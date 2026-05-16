package service

import (
	"context"
	"testing"

	"github.com/RedInn7/gomall/consts"
	"github.com/RedInn7/gomall/pkg/utils/ctl"
)

func TestRefund_StateGuardLogic_Request(t *testing.T) {
	legal := []uint{consts.OrderWaitShip, consts.OrderWaitReceive, consts.OrderCompleted}
	for _, from := range legal {
		if !inUintSlice(from, refundAllowedFrom) {
			t.Fatalf("退款发起期望允许 from=%d", from)
		}
		if !CanTransition(from, consts.OrderRefunding) {
			t.Fatalf("CanTransition(%d, Refunding) 应为 true", from)
		}
	}
	for _, from := range []uint{consts.OrderWaitPay, consts.OrderClosed, consts.OrderRefunded, consts.OrderRefunding} {
		if inUintSlice(from, refundAllowedFrom) {
			t.Fatalf("退款发起不应允许 from=%d", from)
		}
	}
}

func TestRefund_StateGuardLogic_Approve(t *testing.T) {
	if !CanTransition(consts.OrderRefunding, consts.OrderRefunded) {
		t.Fatal("Refunding -> Refunded 必须合法")
	}
	for _, illegal := range []uint{consts.OrderWaitPay, consts.OrderWaitShip, consts.OrderWaitReceive, consts.OrderCompleted, consts.OrderClosed, consts.OrderRefunded} {
		if CanTransition(illegal, consts.OrderRefunded) {
			t.Fatalf("非法转换 %d -> Refunded 应被拒绝", illegal)
		}
	}
}

func TestRefund_StateGuardLogic_Reject(t *testing.T) {
	if !CanTransition(consts.OrderRefunding, consts.OrderCompleted) {
		t.Fatal("Refunding -> Completed (reject) 必须合法")
	}
}

// TestRefund_RequestRequiresUser 用户上下文缺失时不会动 DB，直接报错。
func TestRefund_RequestRequiresUser(t *testing.T) {
	initLogForTest()
	if err := GetRefundSrv().RequestRefund(context.Background(), 1, "no ctx"); err == nil {
		t.Fatal("expected error when ctx has no user")
	}
}

// TestRefund_DBMissing 验证 service 入口在 DB 未初始化时不会 panic。
func TestRefund_DBMissing(t *testing.T) {
	initLogForTest()
	ctx := ctl.NewContext(context.Background(), &ctl.UserInfo{Id: 1})
	if err := safeCall(func() error { return GetRefundSrv().RequestRefund(ctx, 99999, "reason") }); err == nil {
		t.Fatal("expected error when DB not initialized (request)")
	}
	if err := safeCall(func() error { return GetRefundSrv().ApproveRefund(ctx, 99999) }); err == nil {
		t.Fatal("expected error when DB not initialized (approve)")
	}
	if err := safeCall(func() error { return GetRefundSrv().RejectRefund(ctx, 99999, "no") }); err == nil {
		t.Fatal("expected error when DB not initialized (reject)")
	}
}

func TestRefund_InUintSliceHelper(t *testing.T) {
	if !inUintSlice(2, []uint{1, 2, 3}) {
		t.Fatal("inUintSlice should find present value")
	}
	if inUintSlice(7, []uint{1, 2, 3}) {
		t.Fatal("inUintSlice should not find missing value")
	}
	if inUintSlice(1, nil) {
		t.Fatal("inUintSlice on nil should return false")
	}
}
