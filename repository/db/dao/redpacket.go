package dao

import (
	"context"
	"time"

	"gorm.io/gorm"

	"github.com/RedInn7/gomall/repository/db/model"
)

type RedPacketDao struct {
	*gorm.DB
}

func NewRedPacketDao(ctx context.Context) *RedPacketDao {
	return &RedPacketDao{NewDBClient(ctx)}
}

func NewRedPacketDaoByDB(db *gorm.DB) *RedPacketDao {
	return &RedPacketDao{db}
}

// Create 写入红包主记录
func (d *RedPacketDao) Create(rp *model.RedPacket) error {
	return d.DB.Create(rp).Error
}

// Get 主键查询
func (d *RedPacketDao) Get(id uint) (*model.RedPacket, error) {
	var rp model.RedPacket
	if err := d.DB.First(&rp, id).Error; err != nil {
		return nil, err
	}
	return &rp, nil
}

// CreateClaim 写入领取记录 (uniq 索引可拦同用户重复)
func (d *RedPacketDao) CreateClaim(c *model.RedPacketClaim) error {
	return d.DB.Create(c).Error
}

// DecrRemaining remaining-- (用 SQL 表达式避免覆盖写)
//
//	保护条件 remaining > 0，避免减出负数
func (d *RedPacketDao) DecrRemaining(id uint) (int64, error) {
	res := d.DB.Model(&model.RedPacket{}).
		Where("id = ? AND remaining > 0", id).
		Update("remaining", gorm.Expr("remaining - 1"))
	return res.RowsAffected, res.Error
}

// MarkStatus 状态切换 (active->finished/expired/refunded)
func (d *RedPacketDao) MarkStatus(id uint, status uint) error {
	return d.DB.Model(&model.RedPacket{}).
		Where("id = ?", id).
		Update("status", status).Error
}

// ListMine 我发出的红包，按 id desc 分页
func (d *RedPacketDao) ListMine(userID uint, lastID uint, pageSize int) ([]*model.RedPacket, error) {
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 20
	}
	q := d.DB.Model(&model.RedPacket{}).Where("user_id = ?", userID)
	if lastID > 0 {
		q = q.Where("id < ?", lastID)
	}
	var out []*model.RedPacket
	err := q.Order("id DESC").Limit(pageSize).Find(&out).Error
	return out, err
}

// ListClaims 红包下的领取明细 (用于详情页)
func (d *RedPacketDao) ListClaims(redPacketID uint) ([]*model.RedPacketClaim, error) {
	var out []*model.RedPacketClaim
	err := d.DB.Where("red_packet_id = ?", redPacketID).
		Order("id ASC").
		Find(&out).Error
	return out, err
}

// GetExpired 取出 status=active 且 expire_at <= now 的红包，限量
func (d *RedPacketDao) GetExpired(limit int) ([]*model.RedPacket, error) {
	var out []*model.RedPacket
	err := d.DB.Where("status = ? AND expire_at <= ?",
		model.RedPacketStatusActive, time.Now()).
		Limit(limit).
		Find(&out).Error
	return out, err
}
