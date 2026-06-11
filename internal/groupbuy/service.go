package groupbuy

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"gorm.io/gorm"

	"github.com/RedInn7/gomall/consts"
	orderpkg "github.com/RedInn7/gomall/internal/order"
	"github.com/RedInn7/gomall/internal/shared/outbox"
	util "github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/pkg/utils/snowflake"
	"github.com/RedInn7/gomall/repository/cache"
	"github.com/RedInn7/gomall/repository/db/dao"
	"github.com/RedInn7/gomall/service/events"
)

// 拼团默认时效。业务承诺：截单线 24h，运营可在 routes 入口覆盖。
const DefaultGroupbuyTTL = 24 * time.Hour

// 业务错误：service 层向 handler / cron 返回，由 handler 翻成 81001-81004
// 业务码与客服话术（pkg/e/msg.go）。
var (
	ErrGroupbuyFull          = errors.New("拼团已满员")
	ErrGroupbuyExpired       = errors.New("拼团已超时")
	ErrGroupbuyClosed        = errors.New("拼团已关闭")
	ErrGroupbuyDuplicateJoin = errors.New("您已加入该团")
	ErrGroupbuyNotFound      = errors.New("拼团不存在")
)

var (
	GroupbuySrvIns  *GroupbuySrv
	GroupbuySrvOnce sync.Once
)

// GroupbuySrv 拼团域服务。所有写路径都在 tx 内组合：
//
//	业务表 + 订单 + outbox + Redis 库存预扣 / 真扣 / 归还
//
// 库存复用 cache/inventory 的 Reserve / Commit / Release 三个 Lua，
// 不为拼团另写 script，保持库存口径单一。
type GroupbuySrv struct{}

func GetGroupbuySrv() *GroupbuySrv {
	GroupbuySrvOnce.Do(func() {
		GroupbuySrvIns = &GroupbuySrv{}
	})
	return GroupbuySrvIns
}

// CreateGroupResp 给 handler / 前端返回的发起结果。
type CreateGroupResp struct {
	GroupID  uint   `json:"group_id"`
	OrderID  uint   `json:"order_id"`
	OrderNum uint64 `json:"order_num"`
	ExpireAt int64  `json:"expire_at"` // unix
}

// JoinGroupResp 加入结果，is_success=true 表示本次加入凑齐成团。
type JoinGroupResp struct {
	GroupID      uint   `json:"group_id"`
	OrderID      uint   `json:"order_id"`
	OrderNum     uint64 `json:"order_num"`
	CurrentCount int    `json:"current_count"`
	TargetCount  int    `json:"target_count"`
	IsSuccess    bool   `json:"is_success"`
}

// CreateGroup 团长发起：
//  1. 校验入参 + 预扣 1 份库存（available → reserved）
//  2. tx 内：写 group / 写 leader 订单 (WaitGroup) / 写 member / outbox(groupbuy.created)
//  3. tx 失败 → 释放预扣
func (s *GroupbuySrv) CreateGroup(ctx context.Context, leaderID, productID uint, targetCount int, priceCents int64, ttl time.Duration, bossID, addressID uint) (*CreateGroupResp, error) {
	if targetCount < 2 {
		return nil, errors.New("成团人数至少 2")
	}
	if priceCents <= 0 {
		return nil, errors.New("拼团价非法")
	}
	if ttl <= 0 {
		ttl = DefaultGroupbuyTTL
	}

	// 1. 预扣 1 份库存
	if err := cache.ReserveStock(ctx, productID, 1); err != nil {
		util.LogrusObj.Errorf("groupbuy reserve stock leader=%d product=%d err=%v", leaderID, productID, err)
		return nil, err
	}

	expireAt := time.Now().Add(ttl)
	g := &GroupbuyGroup{
		ProductID:    productID,
		LeaderID:     leaderID,
		TargetCount:  targetCount,
		CurrentCount: 1, // 团长自己已算 1 人
		PriceCents:   priceCents,
		Status:       GroupbuyStatusOpen,
		ExpireAt:     expireAt,
	}

	leaderOrder := &orderpkg.Order{
		UserID:    leaderID,
		ProductID: productID,
		BossID:    bossID,
		AddressID: addressID,
		Num:       1,
		OrderNum:  uint64(snowflake.GenSnowflakeID()),
		Type:      consts.OrderWaitGroup,
		Money:     priceCents,
	}

	err := dao.NewDBClient(ctx).Transaction(func(tx *gorm.DB) error {
		if e := NewGroupbuyDaoByDB(tx).CreateGroup(g); e != nil {
			return e
		}
		if e := orderpkg.NewOrderDaoByDB(tx).CreateOrder(leaderOrder); e != nil {
			return e
		}
		member := &GroupbuyMember{
			GroupID: g.ID,
			UserID:  leaderID,
			OrderID: int64(leaderOrder.ID),
			Status:  GroupbuyMemberJoined,
		}
		if e := tx.Create(member).Error; e != nil {
			return e
		}
		return outbox.NewOutboxDaoByDB(tx).Insert(
			"groupbuy", "GroupbuyCreated", "groupbuy.created", g.ID,
			events.GroupbuyCreated{
				GroupID:     g.ID,
				ProductID:   productID,
				LeaderID:    leaderID,
				TargetCount: targetCount,
				PriceCents:  priceCents,
				ExpireAt:    expireAt.Unix(),
			},
		)
	})
	if err != nil {
		// Saga 回滚：释放预扣，避免库存泄漏
		if relErr := cache.ReleaseReservation(ctx, productID, 1); relErr != nil {
			util.LogrusObj.Errorf("groupbuy release on create failure product=%d err=%v", productID, relErr)
		}
		return nil, err
	}

	return &CreateGroupResp{
		GroupID:  g.ID,
		OrderID:  leaderOrder.ID,
		OrderNum: leaderOrder.OrderNum,
		ExpireAt: expireAt.Unix(),
	}, nil
}

// JoinGroup 成员加入。
//  1. 提前探测重复加入（友好 81003），真兜底由 DB uniqueIndex 抓
//  2. 预扣 1 份库存
//  3. tx 内：JoinGroupAtomic（单 SQL 抢名额）+ 写订单 (WaitGroup) + outbox(groupbuy.joined)
//  4. 若加完 current=target → 同事务外再调 markGroupSuccess 推进
//  5. 任一失败 → 释放预扣
func (s *GroupbuySrv) JoinGroup(ctx context.Context, userID, groupID, bossID, addressID uint) (*JoinGroupResp, error) {
	gbDao := NewGroupbuyDao(ctx)
	g, err := gbDao.GetGroupByID(groupID)
	if err != nil {
		return nil, err
	}
	if g == nil {
		return nil, ErrGroupbuyNotFound
	}
	// 状态前置校验，给出准确业务码
	switch g.Status {
	case GroupbuyStatusSuccess, GroupbuyStatusClosed:
		return nil, ErrGroupbuyClosed
	case GroupbuyStatusExpired:
		return nil, ErrGroupbuyExpired
	}
	if !g.ExpireAt.After(time.Now()) {
		return nil, ErrGroupbuyExpired
	}
	if g.CurrentCount >= g.TargetCount {
		return nil, ErrGroupbuyFull
	}
	if joined, e := gbDao.HasUserJoined(groupID, userID); e == nil && joined {
		return nil, ErrGroupbuyDuplicateJoin
	}

	// 1. 预扣 1 份库存
	if err = cache.ReserveStock(ctx, g.ProductID, 1); err != nil {
		util.LogrusObj.Errorf("groupbuy join reserve stock user=%d product=%d err=%v", userID, g.ProductID, err)
		return nil, err
	}

	order := &orderpkg.Order{
		UserID:    userID,
		ProductID: g.ProductID,
		BossID:    bossID,
		AddressID: addressID,
		Num:       1,
		OrderNum:  uint64(snowflake.GenSnowflakeID()),
		Type:      consts.OrderWaitGroup,
		Money:     g.PriceCents,
	}

	var (
		atomicErr   error
		currentCnt  int
		targetCount = g.TargetCount
	)
	err = dao.NewDBClient(ctx).Transaction(func(tx *gorm.DB) error {
		member := &GroupbuyMember{
			GroupID: groupID,
			UserID:  userID,
			Status:  GroupbuyMemberJoined,
		}
		// 先订单后 member：order.ID 需要回填到 member.OrderID
		if e := orderpkg.NewOrderDaoByDB(tx).CreateOrder(order); e != nil {
			return e
		}
		member.OrderID = int64(order.ID)

		// 原子抢名额 + 写 member 行；成员行的 uniqueIndex 会把"重复加入"在 DB 层兜底
		if e := NewGroupbuyDaoByDB(tx).JoinGroupAtomic(groupID, member); e != nil {
			atomicErr = e
			return e
		}

		// 读最新 count，用于响应 + 判定是否成团
		freshGroup, e := NewGroupbuyDaoByDB(tx).GetGroupByID(groupID)
		if e != nil {
			return e
		}
		if freshGroup != nil {
			currentCnt = freshGroup.CurrentCount
		}

		return outbox.NewOutboxDaoByDB(tx).Insert(
			"groupbuy", "GroupbuyJoined", "groupbuy.joined", groupID,
			events.GroupbuyJoined{
				GroupID:      groupID,
				UserID:       userID,
				OrderID:      int64(order.ID),
				CurrentCount: currentCnt,
				TargetCount:  targetCount,
			},
		)
	})
	if err != nil {
		// Saga 回滚：释放本次预扣
		if relErr := cache.ReleaseReservation(ctx, g.ProductID, 1); relErr != nil {
			util.LogrusObj.Errorf("groupbuy release on join failure group=%d err=%v", groupID, relErr)
		}
		// JoinGroupAtomic 抛出 ErrGroupbuyFull 时，需要再读一次团状态决定真正业务码
		if atomicErr != nil && errors.Is(atomicErr, ErrGroupbuyFull) {
			latest, _ := gbDao.GetGroupByID(groupID)
			if latest != nil {
				switch latest.Status {
				case GroupbuyStatusClosed, GroupbuyStatusSuccess:
					return nil, ErrGroupbuyClosed
				case GroupbuyStatusExpired:
					return nil, ErrGroupbuyExpired
				}
				if !latest.ExpireAt.After(time.Now()) {
					return nil, ErrGroupbuyExpired
				}
			}
			return nil, ErrGroupbuyFull
		}
		return nil, err
	}

	isSuccess := false
	if currentCnt >= targetCount {
		if mkErr := s.MarkGroupSuccess(ctx, groupID); mkErr != nil {
			// 成团失败只记录日志，cron 仍会兜底
			util.LogrusObj.Errorf("mark group success group=%d err=%v", groupID, mkErr)
		} else {
			isSuccess = true
		}
	}

	return &JoinGroupResp{
		GroupID:      groupID,
		OrderID:      order.ID,
		OrderNum:     order.OrderNum,
		CurrentCount: currentCnt,
		TargetCount:  targetCount,
		IsSuccess:    isSuccess,
	}, nil
}

// MarkGroupSuccess 凑齐 N 人后的成团：
//
//	tx 内：MarkGroupSuccessIfFull + 成员订单 WaitGroup → WaitShip + member.status=succeed
//	+ outbox(groupbuy.success)；tx 外：库存 reserved→sold（每个成员 1 份）
//
// 多次调用幂等：第一次切 status，之后 MarkGroupSuccessIfFull 返回 false 直接 no-op。
func (s *GroupbuySrv) MarkGroupSuccess(ctx context.Context, groupID uint) error {
	gbDao := NewGroupbuyDao(ctx)
	g, err := gbDao.GetGroupByID(groupID)
	if err != nil {
		return err
	}
	if g == nil {
		return ErrGroupbuyNotFound
	}

	var (
		switched bool
		members  []*GroupbuyMember
	)
	err = dao.NewDBClient(ctx).Transaction(func(tx *gorm.DB) error {
		ok, e := NewGroupbuyDaoByDB(tx).MarkGroupSuccessIfFull(groupID)
		if e != nil {
			return e
		}
		if !ok {
			return nil
		}
		switched = true

		members, e = NewGroupbuyDaoByDB(tx).ListMembers(groupID)
		if e != nil {
			return e
		}

		// 成员订单 WaitGroup → WaitShip。逐条 UPDATE 保证条件 WHERE 命中状态，
		// 任一失败回滚整个成团操作，cron 重新拉起。
		for _, m := range members {
			res := tx.Model(&orderpkg.Order{}).
				Where("id=? AND type=?", m.OrderID, consts.OrderWaitGroup).
				Update("type", consts.OrderWaitShip)
			if res.Error != nil {
				return res.Error
			}
			if res.RowsAffected == 0 {
				return fmt.Errorf("成员订单状态异常 orderID=%d", m.OrderID)
			}
		}

		if e = NewGroupbuyDaoByDB(tx).UpdateMembersStatus(groupID, GroupbuyMemberSucceed); e != nil {
			return e
		}

		orderIDs := make([]int64, 0, len(members))
		memberIDs := make([]uint, 0, len(members))
		for _, m := range members {
			orderIDs = append(orderIDs, m.OrderID)
			memberIDs = append(memberIDs, m.ID)
		}
		return outbox.NewOutboxDaoByDB(tx).Insert(
			"groupbuy", "GroupbuySuccess", "groupbuy.success", groupID,
			events.GroupbuySuccess{
				GroupID:   groupID,
				ProductID: g.ProductID,
				MemberIDs: memberIDs,
				OrderIDs:  orderIDs,
			},
		)
	})
	if err != nil {
		return err
	}
	if !switched {
		return nil
	}

	// 库存：每位成员 reserved -> sold（真扣）
	for range members {
		if commitErr := cache.CommitReservation(ctx, g.ProductID, 1); commitErr != nil {
			util.LogrusObj.Errorf("commit reservation on group success group=%d product=%d err=%v",
				groupID, g.ProductID, commitErr)
		}
	}
	return nil
}

// ExpireGroup 24h 散团（cron 或人工触发）：
//
//	tx 内：MarkGroupExpired + 成员订单 WaitGroup → Closed + member.status=refunded
//	+ outbox(groupbuy.expired)；tx 外：库存 reserved→available
//
// 散团是协同式 Saga 的一个标准应用，下游钱包按 outbox 退款。
func (s *GroupbuySrv) ExpireGroup(ctx context.Context, groupID uint) error {
	gbDao := NewGroupbuyDao(ctx)
	g, err := gbDao.GetGroupByID(groupID)
	if err != nil {
		return err
	}
	if g == nil {
		return ErrGroupbuyNotFound
	}
	if g.Status != GroupbuyStatusOpen {
		return nil // 已成团 / 已散团 / 已关闭 —— no-op
	}

	var (
		switched bool
		members  []*GroupbuyMember
	)
	err = dao.NewDBClient(ctx).Transaction(func(tx *gorm.DB) error {
		ok, e := NewGroupbuyDaoByDB(tx).MarkGroupExpired(groupID)
		if e != nil {
			return e
		}
		if !ok {
			return nil
		}
		switched = true

		members, e = NewGroupbuyDaoByDB(tx).ListMembers(groupID)
		if e != nil {
			return e
		}
		for _, m := range members {
			res := tx.Model(&orderpkg.Order{}).
				Where("id=? AND type=?", m.OrderID, consts.OrderWaitGroup).
				Update("type", consts.OrderClosed)
			if res.Error != nil {
				return res.Error
			}
			// RowsAffected=0 不报错：可能已被其它路径关掉
		}
		if e = NewGroupbuyDaoByDB(tx).UpdateMembersStatus(groupID, GroupbuyMemberRefunded); e != nil {
			return e
		}

		orderIDs := make([]int64, 0, len(members))
		for _, m := range members {
			orderIDs = append(orderIDs, m.OrderID)
		}
		return outbox.NewOutboxDaoByDB(tx).Insert(
			"groupbuy", "GroupbuyExpired", "groupbuy.expired", groupID,
			events.GroupbuyExpired{
				GroupID:   groupID,
				ProductID: g.ProductID,
				Reason:    "timeout",
				OrderIDs:  orderIDs,
			},
		)
	})
	if err != nil {
		return err
	}
	if !switched {
		return nil
	}

	// Saga：每位成员预扣归还 available
	for range members {
		if relErr := cache.ReleaseReservation(ctx, g.ProductID, 1); relErr != nil {
			util.LogrusObj.Errorf("release reservation on group expire group=%d product=%d err=%v",
				groupID, g.ProductID, relErr)
		}
	}
	return nil
}

// ShowGroup 拼团详情：用于分享落地页 / 客服回看。
func (s *GroupbuySrv) ShowGroup(ctx context.Context, groupID uint) (*GroupbuyGroup, []*GroupbuyMember, error) {
	gbDao := NewGroupbuyDao(ctx)
	g, err := gbDao.GetGroupByID(groupID)
	if err != nil {
		return nil, nil, err
	}
	if g == nil {
		return nil, nil, ErrGroupbuyNotFound
	}
	members, err := gbDao.ListMembers(groupID)
	if err != nil {
		return g, nil, err
	}
	return g, members, nil
}
