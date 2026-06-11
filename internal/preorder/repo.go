package preorder

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"

	"github.com/RedInn7/gomall/consts"
	"github.com/RedInn7/gomall/internal/order"
	"github.com/RedInn7/gomall/repository/db/dao"
)

// PreorderDao 围绕 product_preorder 表 + order 表的预售字段做收敛。
// 状态机推进始终走条件 UPDATE，避免读后写竞态；上层用 RowsAffected 判定是否真正改动。
type PreorderDao struct {
	*gorm.DB
}

func NewPreorderDao(ctx context.Context) *PreorderDao {
	return &PreorderDao{dao.NewDBClient(ctx)}
}

func NewPreorderDaoByDB(db *gorm.DB) *PreorderDao {
	return &PreorderDao{db}
}

// ErrPreorderNotFound 商品未配置预售。上层应转 4xx 提示用户走普通下单。
var ErrPreorderNotFound = errors.New("商品未配置预售")

// GetPreorderByProductID 查商品预售配置。无配置返回 ErrPreorderNotFound（不是 nil + nil）。
func (d *PreorderDao) GetPreorderByProductID(productID uint) (*ProductPreorder, error) {
	var p ProductPreorder
	err := d.DB.Where("product_id=?", productID).First(&p).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrPreorderNotFound
		}
		return nil, err
	}
	return &p, nil
}

// UpsertPreorder 插入或更新预售配置。运营 / 商家后台落地后这里会暴露 API；
// 当前仅供测试与初始化脚本使用。
func (d *PreorderDao) UpsertPreorder(p *ProductPreorder) error {
	var existing ProductPreorder
	err := d.DB.Where("product_id=?", p.ProductID).First(&existing).Error
	if err == nil {
		p.ID = existing.ID
		p.CreatedAt = existing.CreatedAt
		return d.DB.Save(p).Error
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return d.DB.Create(p).Error
	}
	return err
}

// MarkDepositPaid 条件 UPDATE：仅在 preorder_stage=None 时落地，幂等。
// 同时把 deposit_paid_at 写入，order.type 保留 WaitPay（沿用旧消费者口径）。
func (d *PreorderDao) MarkDepositPaid(tx *gorm.DB, orderID uint, paidAt time.Time) (bool, error) {
	res := tx.Model(&order.Order{}).
		Where("id=? AND preorder_stage=?", orderID, PreorderStageNone).
		Updates(map[string]any{
			"preorder_stage":  PreorderStageDepositPaid,
			"deposit_paid_at": paidAt,
		})
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

// MarkFinalPaid 条件 UPDATE：仅在 stage=DepositPaid 且 type=WaitPay 时推进到 WaitShip。
// 一次 SQL 同时改 preorder_stage / final_paid_at / type，三件事原子提交。
func (d *PreorderDao) MarkFinalPaid(tx *gorm.DB, orderID uint, paidAt time.Time) (bool, error) {
	res := tx.Model(&order.Order{}).
		Where("id=? AND preorder_stage=? AND type=?",
			orderID, PreorderStageDepositPaid, consts.OrderWaitPay).
		Updates(map[string]any{
			"preorder_stage": PreorderStageFinalPaid,
			"final_paid_at":  paidAt,
			"type":           consts.OrderWaitShip,
		})
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

// ForfeitDeposit 把"已付定金但逾期未付尾款"的订单一次性收尾：
//   - preorder_stage: 1 -> 3 (Forfeited)
//   - order.type:     WaitPay -> Closed
//
// 条件 WHERE 兜底幂等，重复调用 RowsAffected=0。
func (d *PreorderDao) ForfeitDeposit(tx *gorm.DB, orderID uint) (bool, error) {
	res := tx.Model(&order.Order{}).
		Where("id=? AND preorder_stage=? AND type=?",
			orderID, PreorderStageDepositPaid, consts.OrderWaitPay).
		Updates(map[string]any{
			"preorder_stage": PreorderStageForfeited,
			"type":           consts.OrderClosed,
		})
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

// ResetPreorderOnCancel 定金期内用户主动取消：把订单整体回到"未付且非预售"，库存由调用方释放。
// 沿用 CloseOrderWithCheck 的条件 UPDATE 思路，但额外清空 deposit_paid_at / preorder_stage。
func (d *PreorderDao) ResetPreorderOnCancel(tx *gorm.DB, orderID uint) (bool, error) {
	res := tx.Model(&order.Order{}).
		Where("id=? AND preorder_stage=? AND type=?",
			orderID, PreorderStageDepositPaid, consts.OrderWaitPay).
		Updates(map[string]any{
			"preorder_stage":  PreorderStageNone,
			"deposit_paid_at": nil,
			"type":            consts.OrderClosed,
		})
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

// ListUnpaidFinalBefore 拉超过 FinalEndAt 仍停在 DepositPaid 的订单，供 cron 没收。
// 上层一次拉一批（limit 防止单次扫表过大），分页通过最后一条 ID 推进。
//
// SQL 注意：product_preorder.final_end_at < ? 走索引，但因为 join 关系，
// 这里把 product_preorder 当作驱动表（按 product_id GROUP）效率更稳。
func (d *PreorderDao) ListUnpaidFinalBefore(now time.Time, limit int) ([]uint, error) {
	if limit <= 0 {
		limit = 200
	}
	var orderIDs []uint
	err := d.DB.Table("`order` as o").
		Joins("INNER JOIN product_preorder pp ON pp.product_id = o.product_id AND pp.deleted_at IS NULL").
		Where("o.preorder_stage = ? AND o.type = ? AND pp.final_end_at < ?",
			PreorderStageDepositPaid, consts.OrderWaitPay, now).
		Limit(limit).
		Order("o.id ASC").
		Pluck("o.id", &orderIDs).Error
	if err != nil {
		return nil, err
	}
	return orderIDs, nil
}
