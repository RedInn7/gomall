package initialize

import (
	"context"
	"fmt"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/RedInn7/gomall/internal/coupon"
	"github.com/RedInn7/gomall/internal/groupbuy"
	"github.com/RedInn7/gomall/internal/order"
	"github.com/RedInn7/gomall/internal/preorder"
	"github.com/RedInn7/gomall/internal/redpacket"
	"github.com/RedInn7/gomall/internal/refund"
	"github.com/RedInn7/gomall/internal/skill"
	util "github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/repository/rabbitmq"
	"github.com/RedInn7/gomall/service/inventory"
)

// InitCron 注册并启动定时任务，返回 cron 实例：退出时 <-c.Stop().Done() 等在跑的任务结束。
func InitCron() *cron.Cron {
	c := cron.New(cron.WithSeconds())
	orderService := new(order.OrderTaskService)
	// @every 5m 而非 "* */5 * * * *"：后者在 WithSeconds 下是「分钟能被 5 整除时每秒触发」，
	// 即 60×/5min，是项目里已知的 cron 陷阱（见下方 GroupbuyExpire 注释）。
	_, err := c.AddFunc("@every 5m", func() {
		defer func() {
			if r := recover(); r != nil {
				util.LogrusObj.Errorf("Cron 任务发生 Panic: %v", r)
			}
		}()
		orderService.RunOrderTimeoutCheck()
	})
	if err != nil {
		panic(fmt.Sprintf("Cron 初始化失败: %v", err))
	}

	// 库存预占对账兜底：每 5min 重算 Redis reserved 与 DB WaitPay 订单的差额，
	// 回收「Redis 占了、DB 无订单」的孤儿预占（崩溃在双写之间留下、其它关单路径
	// 都救不回来的永久泄漏）。任务内部采两次样取交集，避开建单在途的假阳性。
	_, err = c.AddFunc("@every 5m", func() {
		defer func() {
			if r := recover(); r != nil {
				util.LogrusObj.Errorf("StockReconcile Cron Panic: %v", r)
			}
		}()
		orderService.RunStockReservationReconcile()
	})
	if err != nil {
		panic(fmt.Sprintf("StockReconcile Cron 初始化失败: %v", err))
	}

	// available 桶对账：按 DB product.Num 纠正 Redis 丢键重建、退款回补 / 改库存写 Redis 失败留下的漂移。
	// 与上面的 reserved 对账互补（那边只回收多占的预占）。
	_, _ = c.AddFunc("@every 10m", func() {
		defer func() {
			if r := recover(); r != nil {
				util.LogrusObj.Errorf("AvailableReconcile Cron Panic: %v", r)
			}
		}()
		if err := inventory.ReconcileAvailable(context.Background(), 500); err != nil {
			util.LogrusObj.Errorf("AvailableReconcile 执行失败: %v", err)
		}
	})

	// 秒杀名额对账：按 DB sold / 成单记录补回漏掉的 Redis 归还（崩溃在抢名额与投递之间、归还写 Redis 失败）。
	_, _ = c.AddFunc("@every 5m", func() {
		defer func() {
			if r := recover(); r != nil {
				util.LogrusObj.Errorf("SeckillReconcile Cron Panic: %v", r)
			}
		}()
		skill.RunSeckillReconcile()
	})

	redPacketService := new(redpacket.RedPacketTaskService)
	_, err = c.AddFunc("0 */5 * * * *", func() {
		defer func() {
			if r := recover(); r != nil {
				util.LogrusObj.Errorf("RedPacket Cron Panic: %v", r)
			}
		}()
		redPacketService.RunRedPacketExpireCheck()
	})
	if err != nil {
		panic(fmt.Sprintf("RedPacket Cron 初始化失败: %v", err))
	}

	// 自动确认收货兜底：每小时扫一次（翻页扫到空），把发货超过 7 天的 WaitReceive 订单推进到 Completed
	_, err = c.AddFunc("0 0 * * * *", func() {
		defer func() {
			if r := recover(); r != nil {
				util.LogrusObj.Errorf("AutoConfirmReceive Cron Panic: %v", r)
			}
		}()
		orderService.RunAutoConfirmReceive()
	})
	if err != nil {
		panic(fmt.Sprintf("AutoConfirmReceive Cron 初始化失败: %v", err))
	}

	// 拼团 24h 超时散团兜底：每 5min 扫一次 status=Open && expire_at<=now 的团，
	// 逐个调 ExpireGroup（单团事务，单团失败不阻断其它团 —— 协同式 Saga，
	// 下次 tick 自动补；这里用 @every 5m 而不是 "0 */5 * * * *" 之类的 6 段表达式，
	// 规避项目里已知的 "* */5 * * * *" 误写成 60×/5min 那类陷阱。
	// 业务承诺：散团 SLA 落在 ±5min 窗口内。
	if _, e := c.AddFunc("@every 5m", func() {
		defer func() {
			if r := recover(); r != nil {
				util.LogrusObj.Errorf("GroupbuyExpire Cron Panic: %v", r)
			}
		}()
		runGroupbuyExpireSweep()
	}); e != nil {
		// 静默降级：仅记录日志，不让 cron 注册失败把整个进程拖崩
		util.LogrusObj.Errorf("GroupbuyExpire Cron 注册失败: %v", e)
	}
	_, _ = c.AddFunc("@every 5m", groupbuy.GetGroupbuySrv().RunCompleteFullGroups) // 满员但成团失败的团补成团
	_, _ = c.AddFunc("@every 10m", refund.GetRefundSrv().RunSettleCompensation)    // 资金事件丢失时按订单 / 清算单状态补结算、补退款
	_, _ = c.AddFunc("@every 10m", coupon.GetCouponSrv().RunExpireCoupons)         // 过了有效期的未使用券翻成已过期
	_, _ = c.AddFunc("@every 5m", coupon.GetCouponSrv().RunStockReconcile)         // 券 Redis 库存低于 DB 剩余量时抬回（key 丢失 / 回滚失败）

	// 预售尾款没收兜底：每 1h 扫一次 FinalEndAt 已过但 stage 仍 DepositPaid 的订单，
	// service 层内部已做"扫 + 逐条单事务 + 单条 ERROR log"，cron 层只兜 panic。
	// 业务承诺：没收触发延迟 ≤ 1h。
	if _, e := c.AddFunc("@every 1h", func() {
		defer func() {
			if r := recover(); r != nil {
				util.LogrusObj.Errorf("PreorderForfeit Cron Panic: %v", r)
			}
		}()
		runPreorderForfeitSweep()
	}); e != nil {
		util.LogrusObj.Errorf("PreorderForfeit Cron 注册失败: %v", e)
	}

	c.Start()
	return c
}

// runGroupbuyExpireSweep 拉一批过期 open 团，逐个调 ExpireGroup。
// 单团失败只 ERROR log，不中断 batch —— 留给下次 tick 补，这是 Saga 心法。
func runGroupbuyExpireSweep() {
	ctx := context.Background()
	ids, err := groupbuy.NewGroupbuyDao(ctx).ExpireOpenGroupsBefore(time.Now(), 200)
	if err != nil {
		util.LogrusObj.Errorf("GroupbuyExpire 扫表失败: %v", err)
		return
	}
	if len(ids) == 0 {
		return
	}
	srv := groupbuy.GetGroupbuySrv()
	for _, id := range ids {
		if err := srv.ExpireGroup(ctx, id); err != nil {
			util.LogrusObj.Errorf("ExpireGroup 失败 groupID=%d err=%v", id, err)
			continue
		}
	}
}

// runPreorderForfeitSweep 预售没收兜底：service 已封装好"扫 + 逐条事务"，
// cron 层直接调一次即可。
func runPreorderForfeitSweep() {
	ctx := context.Background()
	if err := preorder.GetPreorderSrv().ForfeitDepositsForUnpaidFinals(ctx); err != nil {
		util.LogrusObj.Errorf("PreorderForfeit 执行失败: %v", err)
	}
}

// InitOrderDelayConsumer 声明延迟队列拓扑并启动消费者
func InitOrderDelayConsumer() {
	if err := rabbitmq.InitOrderDelayTopology(); err != nil {
		util.LogrusObj.Errorf("InitOrderDelayTopology failed: %v", err)
		return
	}
	// 关单失败的毒丸消息超限后转入独立死信队列，需保证 DLX 拓扑已声明（幂等）。
	if err := rabbitmq.InitDeadLetterTopology(); err != nil {
		util.LogrusObj.Errorf("InitDeadLetterTopology failed: %v", err)
		return
	}
	if err := rabbitmq.ConsumeOrderCancelDelay(order.CancelUnpaidOrder); err != nil {
		util.LogrusObj.Errorf("ConsumeOrderCancelDelay failed: %v", err)
	}
}
