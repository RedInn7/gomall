package redpacket

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/RedInn7/gomall/repository/db/dao"
)

type RedPacketDao struct {
	*gorm.DB
}

func NewRedPacketDao(ctx context.Context) *RedPacketDao {
	return &RedPacketDao{dao.NewDBClient(ctx)}
}

func NewRedPacketDaoByDB(db *gorm.DB) *RedPacketDao {
	return &RedPacketDao{db}
}

// Create 写入红包主记录
func (d *RedPacketDao) Create(rp *RedPacket) error {
	return d.DB.Create(rp).Error
}

// Get 主键查询
func (d *RedPacketDao) Get(id uint) (*RedPacket, error) {
	var rp RedPacket
	if err := d.DB.First(&rp, id).Error; err != nil {
		return nil, err
	}
	return &rp, nil
}

// CreateClaim 写入领取记录 (uniq 索引可拦同用户重复)
func (d *RedPacketDao) CreateClaim(c *RedPacketClaim) error {
	return d.DB.Create(c).Error
}

// DecrRemaining remaining-- (用 SQL 表达式避免覆盖写)
//
//	保护条件 remaining > 0，避免减出负数；status=active，已被过期回收的红包不能再领。
//	这条 UPDATE 同时拿红包行锁，与回收事务的 FOR UPDATE 串行：回收先提交则这里影响 0 行，
//	领取先提交则回收的 SUM 能看到这笔领取，剩余金额不会被重复发放。
func (d *RedPacketDao) DecrRemaining(id uint) (int64, error) {
	res := d.DB.Model(&RedPacket{}).
		Where("id = ? AND remaining > 0 AND status = ?", id, RedPacketStatusActive).
		Update("remaining", gorm.Expr("remaining - 1"))
	return res.RowsAffected, res.Error
}

// MarkFinishedIfDrained DB 剩余份数归零时把红包置为已抢完，与最后一笔领取同事务。
func (d *RedPacketDao) MarkFinishedIfDrained(id uint) error {
	return d.DB.Model(&RedPacket{}).
		Where("id = ? AND status = ? AND remaining = 0", id, RedPacketStatusActive).
		Update("status", RedPacketStatusFinished).Error
}

// GetForUpdate 事务内对红包行加 FOR UPDATE 后读取
func (d *RedPacketDao) GetForUpdate(id uint) (*RedPacket, error) {
	var rp RedPacket
	if err := d.DB.Clauses(clause.Locking{Strength: "UPDATE"}).First(&rp, id).Error; err != nil {
		return nil, err
	}
	return &rp, nil
}

// SumClaimed 红包已领总额（分），剩余金额 = Total - SumClaimed
func (d *RedPacketDao) SumClaimed(id uint) (int64, error) {
	var sum int64
	err := d.DB.Model(&RedPacketClaim{}).
		Where("red_packet_id = ?", id).
		Select("COALESCE(SUM(amount), 0)").
		Scan(&sum).Error
	return sum, err
}

// MarkStatus 状态切换 (active->refunded)
func (d *RedPacketDao) MarkStatus(id uint, status uint) error {
	return d.DB.Model(&RedPacket{}).
		Where("id = ?", id).
		Update("status", status).Error
}

// ListMine 我发出的红包，按 id desc 分页
func (d *RedPacketDao) ListMine(userID uint, lastID uint, pageSize int) ([]*RedPacket, error) {
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 20
	}
	q := d.DB.Model(&RedPacket{}).Where("user_id = ?", userID)
	if lastID > 0 {
		q = q.Where("id < ?", lastID)
	}
	var out []*RedPacket
	err := q.Order("id DESC").Limit(pageSize).Find(&out).Error
	return out, err
}

// ListClaims 红包下的领取明细 (用于详情页)
func (d *RedPacketDao) ListClaims(redPacketID uint) ([]*RedPacketClaim, error) {
	var out []*RedPacketClaim
	err := d.DB.Where("red_packet_id = ?", redPacketID).
		Order("id ASC").
		Find(&out).Error
	return out, err
}

// GetClaim 按 (red_packet_id, user_id) 反查领取记录（uniq 约束保证至多一行），用于事件结算定位 ref。
// 未找到返回 (nil, nil)，由调用方判脏事件。
func (d *RedPacketDao) GetClaim(redPacketID, userID uint) (*RedPacketClaim, error) {
	var c RedPacketClaim
	err := d.DB.Where("red_packet_id = ? AND user_id = ?", redPacketID, userID).
		First(&c).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// GetExpired 取出已过期、仍是 active、且按 DB 计算仍有剩余金额（Total > 已领总额）的红包，限量。
// finished 只在领取事务里 DB 剩余份数归零时才标（MarkFinishedIfDrained），一定没有余额，不扫：
// 写成 status<>refunded 会让抢完的红包永远留在扫描范围里，每轮每行都要跑一次 SUM 子查询。
// 发包后 Redis 准备失败的红包仍是 active，照样会被扫到退款。
func (d *RedPacketDao) GetExpired(limit int) ([]*RedPacket, error) {
	var out []*RedPacket
	err := d.DB.Where("status = ? AND expire_at <= ?", RedPacketStatusActive, time.Now()).
		Where("total > (SELECT COALESCE(SUM(c.amount), 0) FROM red_packet_claim c WHERE c.red_packet_id = red_packet.id)").
		Limit(limit).
		Find(&out).Error
	return out, err
}

// ListActive 进行中的红包，供 claimed 对账扫描。按 id 升序取前 limit 个。
// ponytail: 单轮固定上限，进行中红包多到扫不完时改成按 id 游标分批
func (d *RedPacketDao) ListActive(limit int) ([]*RedPacket, error) {
	var out []*RedPacket
	err := d.DB.Where("status = ?", RedPacketStatusActive).Order("id ASC").Limit(limit).Find(&out).Error
	return out, err
}

// ListReceived 我领到的红包，按领取记录 id desc 分页
func (d *RedPacketDao) ListReceived(userID uint, lastID uint, pageSize int) ([]*RedPacketClaim, error) {
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 20
	}
	q := d.DB.Where("user_id = ?", userID)
	if lastID > 0 {
		q = q.Where("id < ?", lastID)
	}
	var out []*RedPacketClaim
	err := q.Order("id DESC").Limit(pageSize).Find(&out).Error
	return out, err
}

// ListByIDs 批量取红包
func (d *RedPacketDao) ListByIDs(ids []uint) ([]*RedPacket, error) {
	var out []*RedPacket
	if len(ids) == 0 {
		return out, nil
	}
	err := d.DB.Where("id IN ?", ids).Find(&out).Error
	return out, err
}
