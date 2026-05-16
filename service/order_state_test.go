package service

import (
	"testing"

	"github.com/RedInn7/gomall/consts"
)

func TestOrderState_LegalTransitions(t *testing.T) {
	legal := []struct {
		from uint
		to   uint
		name string
	}{
		{consts.OrderWaitPay, consts.OrderWaitShip, "wait-pay -> wait-ship"},
		{consts.OrderWaitPay, consts.OrderClosed, "wait-pay -> closed"},
		{consts.OrderWaitShip, consts.OrderWaitReceive, "wait-ship -> wait-receive"},
		{consts.OrderWaitShip, consts.OrderRefunding, "wait-ship -> refunding"},
		{consts.OrderWaitReceive, consts.OrderCompleted, "wait-receive -> completed"},
		{consts.OrderWaitReceive, consts.OrderRefunding, "wait-receive -> refunding"},
		{consts.OrderCompleted, consts.OrderRefunding, "completed -> refunding"},
		{consts.OrderRefunding, consts.OrderRefunded, "refunding -> refunded"},
		{consts.OrderRefunding, consts.OrderCompleted, "refunding -> completed (reject)"},
	}
	for _, c := range legal {
		if !CanTransition(c.from, c.to) {
			t.Fatalf("expected legal %s, got rejected (%d -> %d)", c.name, c.from, c.to)
		}
	}
}

func TestOrderState_IllegalTransitions(t *testing.T) {
	illegal := []struct {
		from uint
		to   uint
		name string
	}{
		{consts.OrderWaitPay, consts.OrderCompleted, "skip pay+ship"},
		{consts.OrderWaitPay, consts.OrderWaitReceive, "skip pay"},
		{consts.OrderWaitPay, consts.OrderRefunding, "unpaid cannot refund"},
		{consts.OrderWaitShip, consts.OrderCompleted, "skip receive"},
		{consts.OrderWaitShip, consts.OrderClosed, "paid cannot close (must refund)"},
		{consts.OrderWaitReceive, consts.OrderClosed, "shipped cannot close"},
		{consts.OrderCompleted, consts.OrderWaitReceive, "completed cannot rollback"},
		{consts.OrderCompleted, consts.OrderClosed, "completed cannot close"},
		{consts.OrderCompleted, consts.OrderRefunded, "completed must go through refunding"},
	}
	for _, c := range illegal {
		if CanTransition(c.from, c.to) {
			t.Fatalf("expected illegal %s, got accepted (%d -> %d)", c.name, c.from, c.to)
		}
	}
}

func TestOrderState_TerminalStatesHaveNoOutEdge(t *testing.T) {
	terminals := []uint{consts.OrderClosed, consts.OrderRefunded}
	candidates := []uint{
		consts.OrderWaitPay, consts.OrderWaitShip, consts.OrderClosed,
		consts.OrderWaitReceive, consts.OrderCompleted, consts.OrderRefunding,
		consts.OrderRefunded,
	}
	for _, from := range terminals {
		if !IsTerminalOrderState(from) {
			t.Fatalf("state %d should be terminal", from)
		}
		for _, to := range candidates {
			if CanTransition(from, to) {
				t.Fatalf("terminal %d should not transition to %d", from, to)
			}
		}
	}
}

func TestOrderState_UnknownFromRejected(t *testing.T) {
	if CanTransition(0, consts.OrderWaitPay) {
		t.Fatal("unknown(0) state should have no transitions")
	}
	if CanTransition(99, consts.OrderCompleted) {
		t.Fatal("unknown(99) state should have no transitions")
	}
}

func TestOrderState_NameLookup(t *testing.T) {
	cases := map[uint]string{
		consts.OrderWaitPay:     "待付款",
		consts.OrderWaitShip:    "待发货",
		consts.OrderClosed:      "已关闭",
		consts.OrderWaitReceive: "已发货待收货",
		consts.OrderCompleted:   "已完成",
		consts.OrderRefunding:   "退款中",
		consts.OrderRefunded:    "已退款",
	}
	for k, want := range cases {
		if got := OrderStateName(k); got != want {
			t.Fatalf("OrderStateName(%d) = %q, want %q", k, got, want)
		}
	}
	if got := OrderStateName(99); got == "已完成" {
		t.Fatalf("unknown state should not match known name: %q", got)
	}
}
