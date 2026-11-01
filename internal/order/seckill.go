package order

import (
	"context"
	"errors"

	"gorm.io/gorm"
)

// SeckillBinder 秒杀活动挂到通用异步建单 / 关单链路上的扩展点。
// skill 依赖 order，order 不能反向 import，由 skill 包在 init 时注册。
type SeckillBinder interface {
	// Price 秒杀单的权威单价（分）与卖家，取自活动表，不信任务消息；商品须仍存在、在售且卖家未变。
	Price(ctx context.Context, seckillID, productID uint) (unitCents int64, bossID uint, err error)
	// BindTx 与建单同事务：条件扣活动 sold、记用户成单数，是 Redis 之外的 DB 兜底。
	BindTx(tx *gorm.DB, o *Order) error
	// UnbindTx 与关单同事务：归还活动 sold 与用户成单数。
	UnbindTx(tx *gorm.DB, o *Order) error
	// Settle 建单已提交：把 ticket 移出秒杀在途集合（名额已由 DB sold 承载）。
	Settle(ctx context.Context, seckillID, userID uint, ticket string)
	// Abort 确定建不了单：按 ticket 归还 Redis 名额，cause 决定还库存还是还用户名额，同一 ticket 只还一次。
	Abort(ctx context.Context, seckillID, userID uint, ticket string, cause error)
	// Return 关单已提交：归还 Redis 库存与用户名额，同一订单只还一次。
	Return(ctx context.Context, o *Order)
}

var seckillBinder SeckillBinder

// RegisterSeckill 注册秒杀扩展点（skill 包 init 调用）。
func RegisterSeckill(b SeckillBinder) { seckillBinder = b }

var errSeckillNotRegistered = errors.New("秒杀模块未注册，无法处理秒杀订单")
