package groupbuy

import (
	"context"
	"testing"

	"github.com/RedInn7/gomall/internal/money"
	"github.com/RedInn7/gomall/pkg/utils/ctl"
)

// 这组用例锁住拼团的真实资金链：加入扣款 → 成团结算给卖家 → 散团原路退款，
// 且全程幂等（重复成团 / 重复散团不重复扣退），并堵住"没收钱就发货"。
// 依赖 sqlite in-memory + Redis（与价格/卖家用例同套路），环境缺失整组 skip。

const (
	gbBoss  = uint(7)
	gbPrice = int64(8000) // 拼团价，落在 [5000,10000] 内
)

// TestGroupbuy_JoinChargesMember_FundsFlow 团长发起 + 成员加入即扣款，钱进托管，未结算给卖家。
func TestGroupbuy_JoinChargesMember_FundsFlow(t *testing.T) {
	initLogForTest()
	rcleanup := setupGroupbuyRedis(t)
	defer rcleanup()
	db, dcleanup := setupGroupbuyDB(t)
	defer dcleanup()
	ensureSnowflakeGroupbuy()

	p := seedGroupbuyProduct(t, db, "100.00", gbBoss, 10)
	seedGroupbuyUserWithID(t, db, 42, 100000) // 团长
	seedGroupbuyUserWithID(t, db, 50, 100000) // 参团
	seedGroupbuyUserWithID(t, db, gbBoss, 0)  // 卖家初始 0
	leaderAddr := seedGroupbuyAddress(t, db, 42)
	joinAddr := seedGroupbuyAddress(t, db, 50)

	ctx := ctl.NewContext(context.Background(), &ctl.UserInfo{Id: 42})
	created, err := GetGroupbuySrv().CreateGroup(ctx, 42, p.ID, 3, gbPrice, 0, gbBoss, leaderAddr)
	if err != nil {
		t.Fatalf("CreateGroup: %v", err)
	}
	// 团长加入即扣款
	if bal := getGroupbuyUserBalance(t, db, 42); bal != 100000-gbPrice {
		t.Fatalf("团长加入后余额=%d, want %d（应扣拼团价）", bal, 100000-gbPrice)
	}

	joinCtx := ctl.NewContext(context.Background(), &ctl.UserInfo{Id: 50})
	if _, err = GetGroupbuySrv().JoinGroup(joinCtx, 50, created.GroupID, gbBoss, joinAddr); err != nil {
		t.Fatalf("JoinGroup: %v", err)
	}
	if bal := getGroupbuyUserBalance(t, db, 50); bal != 100000-gbPrice {
		t.Fatalf("成员加入后余额=%d, want %d（应扣拼团价）", bal, 100000-gbPrice)
	}
	// 钱还在托管，未结算给卖家
	if bal := getGroupbuyUserBalance(t, db, gbBoss); bal != 0 {
		t.Fatalf("未成团卖家余额=%d, want 0（钱应在托管，不能进卖家）", bal)
	}
	// 托管账户两笔 credit（团长 + 成员加入）
	var escrowCredits int64
	db.Model(&money.AccountTransaction{}).
		Where("user_id=? AND direction=? AND biz_type=?", money.ExternalClearingUserID, money.DirectionCredit, BizTypeGroupBuyPay).
		Count(&escrowCredits)
	if escrowCredits != 2 {
		t.Fatalf("托管入账笔数=%d, want 2（团长+成员各一笔）", escrowCredits)
	}
}

// TestGroupbuy_SuccessPaysSeller_NoPayNoShip 凑齐成团：托管款结算给卖家，订单才进 WaitShip。
// 同时验证幂等：再次 MarkGroupSuccess 不重复给卖家加钱。
func TestGroupbuy_SuccessPaysSeller_NoPayNoShip(t *testing.T) {
	initLogForTest()
	rcleanup := setupGroupbuyRedis(t)
	defer rcleanup()
	db, dcleanup := setupGroupbuyDB(t)
	defer dcleanup()
	ensureSnowflakeGroupbuy()

	p := seedGroupbuyProduct(t, db, "100.00", gbBoss, 10)
	seedGroupbuyUserWithID(t, db, 42, 100000)
	seedGroupbuyUserWithID(t, db, 50, 100000)
	seedGroupbuyUserWithID(t, db, gbBoss, 0)
	leaderAddr := seedGroupbuyAddress(t, db, 42)
	joinAddr := seedGroupbuyAddress(t, db, 50)

	ctx := ctl.NewContext(context.Background(), &ctl.UserInfo{Id: 42})
	// 2 人成团：建团 1 人 + 加入 1 人即满
	created, err := GetGroupbuySrv().CreateGroup(ctx, 42, p.ID, 2, gbPrice, 0, gbBoss, leaderAddr)
	if err != nil {
		t.Fatalf("CreateGroup: %v", err)
	}
	joinCtx := ctl.NewContext(context.Background(), &ctl.UserInfo{Id: 50})
	joined, err := GetGroupbuySrv().JoinGroup(joinCtx, 50, created.GroupID, gbBoss, joinAddr)
	if err != nil {
		t.Fatalf("JoinGroup: %v", err)
	}
	if !joined.IsSuccess {
		t.Fatal("第二人加入应触发成团")
	}

	// 卖家收到两份拼团价（团长 + 成员）
	wantSeller := gbPrice * 2
	if bal := getGroupbuyUserBalance(t, db, gbBoss); bal != wantSeller {
		t.Fatalf("成团后卖家余额=%d, want %d（两份托管款结算）", bal, wantSeller)
	}
	// 订单已进 WaitShip（没收钱不可能到这一步）
	var shipCount int64
	db.Table("order").Where("type=?", 2 /* OrderWaitShip */).Count(&shipCount)
	if shipCount != 2 {
		t.Fatalf("WaitShip 订单数=%d, want 2", shipCount)
	}

	// 幂等：再次成团不重复给卖家加钱
	if err = GetGroupbuySrv().MarkGroupSuccess(ctx, created.GroupID); err != nil {
		t.Fatalf("MarkGroupSuccess 重复调用: %v", err)
	}
	if bal := getGroupbuyUserBalance(t, db, gbBoss); bal != wantSeller {
		t.Fatalf("重复成团后卖家余额=%d, want %d（幂等，不得重复结算）", bal, wantSeller)
	}
	var settleCredits int64
	db.Model(&money.AccountTransaction{}).
		Where("user_id=? AND direction=? AND biz_type=?", gbBoss, money.DirectionCredit, BizTypeGroupBuySettle).
		Count(&settleCredits)
	if settleCredits != 2 {
		t.Fatalf("卖家结算流水=%d, want 2（每订单一笔，幂等不翻倍）", settleCredits)
	}
}

// TestGroupbuy_ExpireRefundsMembers_Idempotent 散团：托管款原路退回成员，幂等不重复退。
func TestGroupbuy_ExpireRefundsMembers_Idempotent(t *testing.T) {
	initLogForTest()
	rcleanup := setupGroupbuyRedis(t)
	defer rcleanup()
	db, dcleanup := setupGroupbuyDB(t)
	defer dcleanup()
	ensureSnowflakeGroupbuy()

	p := seedGroupbuyProduct(t, db, "100.00", gbBoss, 10)
	seedGroupbuyUserWithID(t, db, 42, 100000)
	seedGroupbuyUserWithID(t, db, 50, 100000)
	seedGroupbuyUserWithID(t, db, gbBoss, 0)
	leaderAddr := seedGroupbuyAddress(t, db, 42)
	joinAddr := seedGroupbuyAddress(t, db, 50)

	ctx := ctl.NewContext(context.Background(), &ctl.UserInfo{Id: 42})
	// 目标 3 人，只来 2 人 → 散团
	created, err := GetGroupbuySrv().CreateGroup(ctx, 42, p.ID, 3, gbPrice, 0, gbBoss, leaderAddr)
	if err != nil {
		t.Fatalf("CreateGroup: %v", err)
	}
	joinCtx := ctl.NewContext(context.Background(), &ctl.UserInfo{Id: 50})
	if _, err = GetGroupbuySrv().JoinGroup(joinCtx, 50, created.GroupID, gbBoss, joinAddr); err != nil {
		t.Fatalf("JoinGroup: %v", err)
	}

	if err = GetGroupbuySrv().ExpireGroup(ctx, created.GroupID); err != nil {
		t.Fatalf("ExpireGroup: %v", err)
	}
	// 两位成员都退回，余额复原
	if bal := getGroupbuyUserBalance(t, db, 42); bal != 100000 {
		t.Fatalf("散团后团长余额=%d, want 100000（应原路退回）", bal)
	}
	if bal := getGroupbuyUserBalance(t, db, 50); bal != 100000 {
		t.Fatalf("散团后成员余额=%d, want 100000（应原路退回）", bal)
	}
	// 卖家不应收到任何钱
	if bal := getGroupbuyUserBalance(t, db, gbBoss); bal != 0 {
		t.Fatalf("散团后卖家余额=%d, want 0（散团不能给卖家打款）", bal)
	}

	// 幂等：重复散团（含消费者自愈路径）不重复退
	if err = GetGroupbuySrv().ExpireGroup(ctx, created.GroupID); err != nil {
		t.Fatalf("重复 ExpireGroup: %v", err)
	}
	if err = GetGroupbuySrv().SettleExpiredRefund(ctx, created.GroupID); err != nil {
		t.Fatalf("SettleExpiredRefund 自愈兜底: %v", err)
	}
	if bal := getGroupbuyUserBalance(t, db, 42); bal != 100000 {
		t.Fatalf("重复散团后团长余额=%d, want 100000（幂等，不得重复退）", bal)
	}
	var refundCredits int64
	db.Model(&money.AccountTransaction{}).
		Where("direction=? AND biz_type=?", money.DirectionCredit, BizTypeGroupBuyRefund).
		Count(&refundCredits)
	if refundCredits != 2 {
		t.Fatalf("成员退款流水=%d, want 2（每订单一笔，幂等不翻倍）", refundCredits)
	}
}
