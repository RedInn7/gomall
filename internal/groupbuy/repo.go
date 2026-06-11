package groupbuy

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"

	"github.com/RedInn7/gomall/repository/db/dao"
)

// 拼团 sentinel error 统一在 service.go 声明（ErrGroupbuyFull / ErrGroupbuyExpired /
// ErrGroupbuyClosed / ErrGroupbuyDuplicateJoin / ErrGroupbuyNotFound），
// handler 据此映射到业务码 81001-81004 并返回客服话术。

type GroupbuyDao struct {
	*gorm.DB
}

func NewGroupbuyDao(ctx context.Context) *GroupbuyDao {
	return &GroupbuyDao{dao.NewDBClient(ctx)}
}

func NewGroupbuyDaoByDB(db *gorm.DB) *GroupbuyDao {
	return &GroupbuyDao{db}
}

// CreateGroup 团长发起拼团；调用方需在事务内组合 CreateOrder + outbox。
func (d *GroupbuyDao) CreateGroup(g *GroupbuyGroup) error {
	return d.DB.Create(g).Error
}

// GetGroupByID 读单条团信息（包含已关 / 已散）。
// 散团的客服解释 / C 端分享落地页都靠这一个查询。
func (d *GroupbuyDao) GetGroupByID(groupID uint) (*GroupbuyGroup, error) {
	var g GroupbuyGroup
	err := d.DB.Model(&GroupbuyGroup{}).Where("id=?", groupID).First(&g).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &g, nil
}

// JoinGroupAtomic 单条 SQL 完成成员加入。
//
//   - 命中条件：team 仍 open / current_count < target / 未过期
//   - RowsAffected=0 → 由 service 根据当前团状态决定返回 81001 / 81002 / 81004
//   - uniqueIndex(group_id, user_id) 兜底 81003 重复加入
//
// 重要：current_count + 1 与 member 行写入必须落同一个 tx，调用方负责。
func (d *GroupbuyDao) JoinGroupAtomic(groupID uint, member *GroupbuyMember) error {
	res := d.DB.Model(&GroupbuyGroup{}).
		Where("id=? AND status=? AND current_count<target_count AND expire_at>?",
			groupID, GroupbuyStatusOpen, time.Now()).
		UpdateColumn("current_count", gorm.Expr("current_count + 1"))
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		// 满 / 超时 / 关 / 不存在 —— service 层根据 group 当前快照判定具体业务码
		return ErrGroupbuyFull
	}
	if err := d.DB.Create(member).Error; err != nil {
		// 唯一索引冲突 → 81003 重复加入
		return err
	}
	return nil
}

// MarkGroupSuccessIfFull 当 current_count >= target_count 时把 status 推到 success。
// 返回 (推进与否, error)；幂等：多次调用只有第一次切 status。
func (d *GroupbuyDao) MarkGroupSuccessIfFull(groupID uint) (bool, error) {
	res := d.DB.Model(&GroupbuyGroup{}).
		Where("id=? AND status=? AND current_count>=target_count",
			groupID, GroupbuyStatusOpen).
		Update("status", GroupbuyStatusSuccess)
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

// MarkGroupExpired 把仍 open 的团切到 expired。返回是否真的切了状态。
// 仅当当前 status=open 时生效，幂等。
func (d *GroupbuyDao) MarkGroupExpired(groupID uint) (bool, error) {
	res := d.DB.Model(&GroupbuyGroup{}).
		Where("id=? AND status=?", groupID, GroupbuyStatusOpen).
		Update("status", GroupbuyStatusExpired)
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

// ExpireOpenGroupsBefore 拉所有"已过期但状态仍 open"的团 id，给 cron 兜底散团用。
// 拉 id 而不是整行：散团本身在 service 内对每个 group 单事务推进。
func (d *GroupbuyDao) ExpireOpenGroupsBefore(now time.Time, limit int) ([]uint, error) {
	if limit <= 0 {
		limit = 100
	}
	var ids []uint
	err := d.DB.Model(&GroupbuyGroup{}).
		Where("status=? AND expire_at<=?", GroupbuyStatusOpen, now).
		Limit(limit).
		Pluck("id", &ids).Error
	return ids, err
}

// ListMembers 列出团内所有成员，按加入时间升序（团长在最前）。
func (d *GroupbuyDao) ListMembers(groupID uint) ([]*GroupbuyMember, error) {
	var rows []*GroupbuyMember
	err := d.DB.Model(&GroupbuyMember{}).
		Where("group_id=?", groupID).
		Order("id ASC").
		Find(&rows).Error
	return rows, err
}

// UpdateMembersStatus 批量把 group 内所有成员订单同步到 newStatus，由成团 / 散团触发。
// 单条 UPDATE，保证一致性。
func (d *GroupbuyDao) UpdateMembersStatus(groupID uint, newStatus uint) error {
	return d.DB.Model(&GroupbuyMember{}).
		Where("group_id=?", groupID).
		Update("status", newStatus).Error
}

// HasUserJoined 提前探测重复加入，给前端在 join 调用前给出友好的 81003 提示。
// 真兜底依赖 uniqueIndex；DB 层 race 时这里读不到也无所谓。
func (d *GroupbuyDao) HasUserJoined(groupID, userID uint) (bool, error) {
	var count int64
	err := d.DB.Model(&GroupbuyMember{}).
		Where("group_id=? AND user_id=?", groupID, userID).
		Count(&count).Error
	if err != nil {
		return false, err
	}
	return count > 0, nil
}
