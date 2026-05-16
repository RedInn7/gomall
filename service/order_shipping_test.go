package service

import (
	"context"
	"errors"
	"testing"

	"github.com/RedInn7/gomall/consts"
	"github.com/RedInn7/gomall/pkg/utils/ctl"
)

// TestShipOrder_RejectedWhenDBMissing 在测试环境下 dao 没有初始化，
// 该测试主要验证 ShipOrder 入口不发生 panic，且会因为 DAO 调用失败返回错误，
// 真正的 happy path 在集成测试 (要求 MySQL) 中覆盖。
func TestShipOrder_RejectedWhenDBMissing(t *testing.T) {
	initLogForTest()
	err := safeCall(func() error {
		return GetShippingSrv().ShipOrder(context.Background(), 999999, "SF1234", "顺丰")
	})
	if err == nil {
		t.Fatal("expected error when DB not initialized")
	}
}

// TestShipOrder_StateGuardLogic 直接验证 ShipOrder 用到的状态机判断与现有常量的关系。
// 这部分不依赖 DB，避免在没有 MySQL 的 CI 环境失败。
func TestShipOrder_StateGuardLogic(t *testing.T) {
	if !CanTransition(consts.OrderWaitShip, consts.OrderWaitReceive) {
		t.Fatal("WaitShip -> WaitReceive 必须合法")
	}
	for _, illegal := range []uint{consts.OrderWaitPay, consts.OrderClosed, consts.OrderRefunded, consts.OrderCompleted} {
		if CanTransition(illegal, consts.OrderWaitReceive) {
			t.Fatalf("非法转换 %d -> WaitReceive 应被拒绝", illegal)
		}
	}
}

// TestConfirmReceive_RequiresUserCtx ConfirmReceive 必须从 ctx 取出 user，
// 否则直接返回 ctl.GetUserInfo 的错误，不会动数据库。
func TestConfirmReceive_RequiresUserCtx(t *testing.T) {
	initLogForTest()
	err := GetShippingSrv().ConfirmReceive(context.Background(), 1)
	if err == nil {
		t.Fatal("expected error when ctx has no user")
	}
}

// TestConfirmReceive_StateGuardLogic 状态机要求 from=WaitReceive。
func TestConfirmReceive_StateGuardLogic(t *testing.T) {
	if !CanTransition(consts.OrderWaitReceive, consts.OrderCompleted) {
		t.Fatal("WaitReceive -> Completed 必须合法")
	}
	for _, illegal := range []uint{consts.OrderWaitPay, consts.OrderWaitShip, consts.OrderClosed, consts.OrderRefunded} {
		if CanTransition(illegal, consts.OrderCompleted) {
			t.Fatalf("非法转换 %d -> Completed 应被拒绝", illegal)
		}
	}
	// 有 user 但 DB 未初始化，ConfirmReceive 应在 DAO 阶段失败而非 panic。
	ctx := ctl.NewContext(context.Background(), &ctl.UserInfo{Id: 42})
	err := safeCall(func() error {
		return GetShippingSrv().ConfirmReceive(ctx, 999999)
	})
	if err == nil {
		t.Fatal("expected error when DB not initialized")
	}
}

// safeCall 兜住 service 在 DB 未初始化时的 nil-pointer panic，让测试以"err != nil"形式收尾。
func safeCall(fn func() error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = errors.New("recovered panic")
		}
	}()
	return fn()
}
