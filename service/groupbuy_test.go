package service

import (
	"context"
	"testing"

	"github.com/RedInn7/gomall/consts"
	"github.com/RedInn7/gomall/pkg/e"
	"github.com/RedInn7/gomall/pkg/utils/ctl"
)

// 拼团相关测试。
// 本仓库 service 包没有 sqlite / sqlmock 基建，集成路径在 docs/architecture 路线图。
// 这里聚焦三件可以在无 DB 环境下覆盖的事：
//  1. 状态机 WaitGroup 出入边正确（合法 / 非法）
//  2. handler 入口在 DB 未初始化时不 panic，只走前置校验
//  3. 业务码 81001-81004 都在 e.MsgFlags 里有客服话术
//
// 真正的端到端用例 —— 团长发起 → 成员加入 → 凑齐 → 转 WaitShip
// 与 24h 散团 → Closed → 库存归还 —— 由 docker-compose 起的 MySQL + Redis
// 跑集成测，写在 stressTest/REPORT.md。

func TestGroupbuy_StateMachine_WaitGroupOut(t *testing.T) {
	if !CanTransition(consts.OrderWaitGroup, consts.OrderWaitShip) {
		t.Fatal("WaitGroup -> WaitShip 必须合法（凑齐 N 人成团）")
	}
	if !CanTransition(consts.OrderWaitGroup, consts.OrderClosed) {
		t.Fatal("WaitGroup -> Closed 必须合法（24h 散团）")
	}
}

func TestGroupbuy_StateMachine_WaitGroupIllegal(t *testing.T) {
	// WaitGroup 不能直接跨到收货 / 退款 / 完成
	for _, illegal := range []uint{
		consts.OrderWaitReceive,
		consts.OrderCompleted,
		consts.OrderRefunding,
		consts.OrderRefunded,
	} {
		if CanTransition(consts.OrderWaitGroup, illegal) {
			t.Fatalf("WaitGroup -> %d 应为非法", illegal)
		}
	}

	// 终态不能反向回到 WaitGroup
	for _, term := range []uint{consts.OrderClosed, consts.OrderRefunded} {
		if CanTransition(term, consts.OrderWaitGroup) {
			t.Fatalf("终态 %d 不应能切回 WaitGroup", term)
		}
	}

	// 已发货 / 已完成 不能反向到 WaitGroup
	for _, paid := range []uint{consts.OrderWaitShip, consts.OrderWaitReceive, consts.OrderCompleted} {
		if CanTransition(paid, consts.OrderWaitGroup) {
			t.Fatalf("%d -> WaitGroup 应为非法（不能回退到拼团中）", paid)
		}
	}
}

func TestGroupbuy_StateName(t *testing.T) {
	if got := OrderStateName(consts.OrderWaitGroup); got != "拼团中" {
		t.Fatalf("OrderStateName(WaitGroup) = %q, want 拼团中", got)
	}
}

// TestGroupbuy_BusinessCodes 81001-81004 必须挂客服话术。
// 接客服 / 工单系统的同事看到 code 就直接复述话术，不能漏。
func TestGroupbuy_BusinessCodes(t *testing.T) {
	codes := []int{
		e.ErrGroupbuyFull,
		e.ErrGroupbuyExpired,
		e.ErrGroupbuyDuplicateJoin,
		e.ErrGroupbuyClosed,
	}
	for _, code := range codes {
		msg, ok := e.MsgFlags[code]
		if !ok || msg == "" {
			t.Fatalf("业务码 %d 缺客服话术", code)
		}
		// 不能 fallback 到 "fail"
		if msg == e.MsgFlags[e.ERROR] {
			t.Fatalf("业务码 %d 不能复用 ERROR 文案", code)
		}
	}
}

// TestGroupbuy_CreateGroup_ParamGuards CreateGroup 在 reserveStock 之前的入参校验
// 不需要 DB / Redis，可以直接跑。
func TestGroupbuy_CreateGroup_ParamGuards(t *testing.T) {
	initLogForTest()
	ctx := ctl.NewContext(context.Background(), &ctl.UserInfo{Id: 1})

	// targetCount < 2 直接拒
	if _, err := GetGroupbuySrv().CreateGroup(ctx, 1, 100, 1, 9900, 0, 0, 0); err == nil {
		t.Fatal("targetCount=1 必须被拒")
	}
	// price <= 0 拒
	if _, err := GetGroupbuySrv().CreateGroup(ctx, 1, 100, 3, 0, 0, 0, 0); err == nil {
		t.Fatal("priceCents=0 必须被拒")
	}
	if _, err := GetGroupbuySrv().CreateGroup(ctx, 1, 100, 3, -100, 0, 0, 0); err == nil {
		t.Fatal("priceCents<0 必须被拒")
	}
}

// TestGroupbuy_JoinGroup_NoDBNoPanic 当 DB 未初始化时 JoinGroup 不能 panic，
// 应走 GetGroupByID 的错误路径返回。
func TestGroupbuy_JoinGroup_NoDBNoPanic(t *testing.T) {
	initLogForTest()
	err := safeCall(func() error {
		_, e := GetGroupbuySrv().JoinGroup(context.Background(), 42, 999999, 0, 0)
		return e
	})
	if err == nil {
		t.Fatal("expected error when DB not initialized")
	}
}

// TestGroupbuy_ExpireGroup_NoDBNoPanic 同上，散团入口在 DB 缺位时也不能挂。
func TestGroupbuy_ExpireGroup_NoDBNoPanic(t *testing.T) {
	initLogForTest()
	err := safeCall(func() error {
		return GetGroupbuySrv().ExpireGroup(context.Background(), 999999)
	})
	if err == nil {
		t.Fatal("expected error when DB not initialized")
	}
}

// TestGroupbuy_DefaultTTLFallback CreateGroup 入参 ttl=0 应回落到 24h；这里通过
// 直接读常量验证，避免在 reserveStock 阶段失败前就触发 24h 计算。
func TestGroupbuy_DefaultTTL(t *testing.T) {
	if DefaultGroupbuyTTL.Hours() != 24 {
		t.Fatalf("DefaultGroupbuyTTL = %v, want 24h", DefaultGroupbuyTTL)
	}
}
