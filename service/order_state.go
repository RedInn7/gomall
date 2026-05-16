package service

import (
	"errors"
	"fmt"

	"github.com/RedInn7/gomall/consts"
)

// orderStateTransitions 描述合法的订单状态转换表。
// key 是 from 状态，value 是允许的 to 状态集合。
//
//	WaitPay     → WaitShip(支付成功) / Closed(取消、超时)
//	WaitShip    → WaitReceive(发货) / Refunding(已付未发起退)
//	WaitReceive → Completed(确认收货 / 7d 自动) / Refunding(发起退货)
//	Completed   → Refunding(售后期内申请)
//	Refunding   → Refunded(同意退款) / Completed(驳回回到完成)
//
// Closed / Refunded 是终态，无出边。
var orderStateTransitions = map[uint][]uint{
	consts.OrderWaitPay:     {consts.OrderWaitShip, consts.OrderClosed},
	consts.OrderWaitShip:    {consts.OrderWaitReceive, consts.OrderRefunding},
	consts.OrderWaitReceive: {consts.OrderCompleted, consts.OrderRefunding},
	consts.OrderCompleted:   {consts.OrderRefunding},
	consts.OrderRefunding:   {consts.OrderRefunded, consts.OrderCompleted},
}

// ErrInvalidOrderStateTransition 表示当前订单状态不允许迁移到目标状态。
// 上层应当把它转成 4xx 业务错误，而不是 5xx。
var ErrInvalidOrderStateTransition = errors.New("非法订单状态转换")

// CanTransition 校验 from -> to 是否在合法转换表里。
// 终态 (Closed / Refunded) 没有出边，任何调用都返回 false。
func CanTransition(from, to uint) bool {
	allowed, ok := orderStateTransitions[from]
	if !ok {
		return false
	}
	for _, s := range allowed {
		if s == to {
			return true
		}
	}
	return false
}

// OrderStateName 返回订单状态的业务展示名。未知状态返回 unknown(数值)。
func OrderStateName(s uint) string {
	if n, ok := consts.OrderStateMap[s]; ok {
		return n
	}
	return fmt.Sprintf("unknown(%d)", s)
}

// IsTerminalOrderState 判断是否为终态。终态订单不应再被状态机驱动。
func IsTerminalOrderState(s uint) bool {
	_, ok := orderStateTransitions[s]
	return !ok
}
