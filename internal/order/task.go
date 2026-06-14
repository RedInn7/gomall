package order

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"

	"github.com/RedInn7/gomall/consts"
	"github.com/RedInn7/gomall/internal/shared/outbox"
	util "github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/repository/cache"
	"github.com/RedInn7/gomall/service/events"
)

// autoConfirmReceiveDays 已发货订单超过这个天数仍未确认收货，cron 兜底自动确认。
// 默认 7 天，与主流电商口径一致；商家自定义在 routes/admin 后台落地后切换。
const autoConfirmReceiveDays = 7

type OrderTaskService struct {
}

func (s *OrderTaskService) RunOrderTimeoutCheck() {
	baseDao := NewOrderDao(context.Background())
	orders, err := baseDao.GetTimeoutOrders(15, 100)
	if err != nil {
		util.LogrusObj.Errorf("Cron Job Error: fetch orders failed: %v\n", err)
		return
	}

	for _, order := range orders {
		// 走与 MQ 延迟取消(CancelUnpaidOrder)完全相同的幂等逻辑：
		// 关单 + 写 order.cancelled 事件 + 释放 Redis reserved 预占。
		// 这是 DB 驱动的兜底：建单成功后 PublishOrderCancelDelay 若因崩溃丢消息，
		// 这里仍能扫到 WaitPay 订单补偿关单并退还预占。
		// 不再回写 product.Num —— 未支付订单从未真正扣减过 DB 库存，回写会虚高
		// （与旧实现的关键差异，旧实现既漏退 Redis 又错误地抬高 DB 水位）。
		if err := CancelUnpaidOrder(order.OrderNum); err != nil {
			util.LogrusObj.Errorf("关单失败 orderNum=%v err=%v", order.OrderNum, err)
			continue
		}
		util.LogrusObj.Infof("orderNum:%v 关单成功", order.OrderNum)
	}
}

// stockReconcileGrace 对账两次采样之间的静默期：必须远大于建单临界区
// （ReserveStock -> DB commit 通常亚秒级），让「建单在途」的预占在二次采样时
// 已落库，避免把它误判成泄漏而错误回收。取 5s 留足余量。
const stockReconcileGrace = 5 * time.Second

// stockCommitGrace 支付「DB 已 WaitShip、Redis reserved 尚未 CommitReservation」窗口的宽限期。
// 支付路径先在 DB 事务把订单推进到 WaitShip，再在事务外扣 reserved；这两步之间订单已离开
// WaitPay 但 reserved 仍占着。对账时把「最近 stockCommitGrace 内刚推进到 WaitShip」的订单量
// 并入基准，避免把这笔在途预占误判成泄漏而回收，否则随后支付侧 CommitReservation 会净超卖。
// 取值远大于事务提交到 commit 之间的耗时（通常亚秒级），30s 留足重试 / 抖动余量。
const stockCommitGrace = 30 * time.Second

// RunStockReservationReconcile 对账 Redis reserved 桶与 DB 未支付订单，回收泄漏的预占。
//
// 为什么需要它：下单是「先写 Redis reserved，再提交 DB 订单」的双写，二者没有跨系统
// 原子性。进程在两步之间崩溃会留下「Redis 占了、DB 无订单」的孤儿预占；这种单子既没有
// 订单行（GetTimeoutOrders 扫不到）、也没有延迟消息，任何关单路径都救不回来，库存被永久
// 占死。本任务按商品重算 reserved 应有水位（Σ WaitPay 订单 Num），把多出来的退回 available。
//
// 只回收「多占」（reserved > Σ订单），绝不反向给 reserved 补量 —— 少占可能是 Redis 丢数据，
// 盲目从 available 搬运会超卖。为躲开「建单在途」假阳性，采两次样取交集（min）：真泄漏两次
// 都在，在途预占第二次已落 DB 而消失。
func (s *OrderTaskService) RunStockReservationReconcile() {
	ctx := context.Background()
	leak1, err := s.sampleReservationLeak(ctx)
	if err != nil {
		util.LogrusObj.Errorf("StockReconcile 首次采样失败: %v", err)
		return
	}
	if len(leak1) == 0 {
		return
	}
	time.Sleep(stockReconcileGrace)
	leak2, err := s.sampleReservationLeak(ctx)
	if err != nil {
		util.LogrusObj.Errorf("StockReconcile 二次采样失败: %v", err)
		return
	}

	for pid, l1 := range leak1 {
		leaked := l1
		if l2 := leak2[pid]; l2 < leaked {
			leaked = l2 // 取两次采样交集，过滤掉只在某一次出现的在途预占
		}
		if leaked <= 0 {
			continue
		}
		if err := cache.ReleaseReservation(ctx, pid, leaked); err != nil {
			util.LogrusObj.Errorf("StockReconcile 释放泄漏预占失败 product=%d n=%d err=%v", pid, leaked, err)
			continue
		}
		util.LogrusObj.Warnf("StockReconcile 回收泄漏预占 product=%d n=%d（reserved 超出 WaitPay 订单口径）", pid, leaked)
	}
}

// sampleReservationLeak 采一次样：对每个存在 reserved 桶的商品，算
// reserved - Σ(WaitPay Num) - Σ(刚支付待 commit 的 Num)，正值即疑似泄漏。返回 map[productID]leak(>0)。
//
// 基准里减去两类合法占用：
//   - WaitPay 订单：reserved 的正常归属；
//   - 刚推进到 WaitShip 且在 stockCommitGrace 内的订单：支付已成功但 Redis 尚未 CommitReservation
//     的在途预占。这一项是关键防线，避免把「支付成功但 commit 在途」的预占误回收成泄漏。
func (s *OrderTaskService) sampleReservationLeak(ctx context.Context) (map[uint]int64, error) {
	pids, err := cache.ScanReservedProductIDs(ctx)
	if err != nil {
		return nil, err
	}
	if len(pids) == 0 {
		return nil, nil
	}
	dao := NewOrderDao(ctx)
	waitPaySum, err := dao.SumWaitPayNumByProduct()
	if err != nil {
		return nil, err
	}
	inflightSum, err := s.sumRecentlyPaidNumByProduct(ctx, time.Now().Add(-stockCommitGrace))
	if err != nil {
		return nil, err
	}
	leak := make(map[uint]int64)
	for _, pid := range pids {
		_, reserved, err := cache.GetStockSnapshot(ctx, pid)
		if err != nil {
			util.LogrusObj.Errorf("StockReconcile 读 reserved 失败 product=%d err=%v", pid, err)
			continue
		}
		// 减去 WaitPay 与刚支付待 commit 两类合法占用，剩余才视为疑似泄漏。
		if diff := reserved - waitPaySum[pid] - inflightSum[pid]; diff > 0 {
			leak[pid] = diff
		}
	}
	return leak, nil
}

// sumRecentlyPaidNumByProduct 汇总每个商品「刚支付成功、Redis 预占可能尚未 commit」的在途数量(Σ Num)。
// 口径：type=WaitShip 且 updated_at 在 since 之后（刚由支付推进而来、仍在 commit 宽限期内）。
// 仅取宽限期内的新近订单，避免把早已 commit 完成的 WaitShip 也算进基准而漏掉真实泄漏。
func (s *OrderTaskService) sumRecentlyPaidNumByProduct(ctx context.Context, since time.Time) (map[uint]int64, error) {
	var rows []struct {
		ProductID uint
		Total     int64
	}
	if err := NewOrderDao(ctx).DB.Model(&Order{}).
		Select("product_id, SUM(num) AS total").
		Where("type = ? AND updated_at >= ?", consts.OrderWaitShip, since).
		Group("product_id").
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	m := make(map[uint]int64, len(rows))
	for _, r := range rows {
		m[r.ProductID] = r.Total
	}
	return m, nil
}

// RunAutoConfirmReceive 对长时间未确认收货的订单兜底自动 Completed。
// 行业惯例 7 天，gomall 默认沿用。一次拉一批，单批失败不影响其它订单。
// 同事务推进状态机 + 写 order.completed 事件，下游服务（点评 / 结算 / 数据）由事件驱动。
func (s *OrderTaskService) RunAutoConfirmReceive() {
	ctx := context.Background()
	baseDao := NewOrderDao(ctx)
	orders, err := baseDao.GetTimeoutWaitReceive(autoConfirmReceiveDays, 100)
	if err != nil {
		util.LogrusObj.Errorf("Cron Job Error: fetch wait-receive orders failed: %v", err)
		return
	}
	for _, order := range orders {
		err := baseDao.DB.Transaction(func(tx *gorm.DB) error {
			ok, err := NewOrderDaoByDB(tx).ConfirmReceive(order.OrderNum)
			if err != nil {
				return err
			}
			if !ok {
				return errors.New("自动确认收货失败：订单状态已变更")
			}
			return outbox.NewOutboxDaoByDB(tx).Insert(
				"order", "OrderCompleted", "order.completed", order.ID,
				events.OrderCompletedEvent{
					OrderID:  order.ID,
					OrderNum: order.OrderNum,
					UserID:   order.UserID,
					Auto:     true,
				},
			)
		})
		if err != nil {
			util.LogrusObj.Errorf("自动确认收货失败 orderNum=%v err=%v", order.OrderNum, err)
			continue
		}
		util.LogrusObj.Infof("orderNum:%v 自动确认收货成功", order.OrderNum)
	}
}
