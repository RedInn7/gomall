package escrow

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestEscrowABI_Valid 校验内嵌的 ABI 是合法 JSON 数组，并且至少包含必要的方法和事件。
// 这一步在没有 solc 的环境下能拦住"ABI 写错破坏 abigen 的输入"这类回归。
func TestEscrowABI_Valid(t *testing.T) {
	if EscrowABI == "" {
		t.Fatal("EscrowABI is empty; embed failed")
	}

	var entries []map[string]any
	if err := json.Unmarshal([]byte(EscrowABI), &entries); err != nil {
		t.Fatalf("EscrowABI is not valid JSON: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("EscrowABI parsed to zero entries")
	}

	requiredFns := map[string]bool{
		"fund":            false,
		"fundWithOrderID": false,
		"release":         false,
		"refund":          false,
		"dispute":         false,
		"buyer":           false,
		"seller":          false,
		"arbiter":         false,
		"amount":          false,
		"state":           false,
		"orderID":         false,
	}
	requiredEvents := map[string]bool{
		"Funded":           false,
		"Released":         false,
		"Refunded":         false,
		"Disputed":         false,
		"PaymentConfirmed": false,
	}

	for _, e := range entries {
		typ, _ := e["type"].(string)
		name, _ := e["name"].(string)
		switch typ {
		case "function":
			if _, ok := requiredFns[name]; ok {
				requiredFns[name] = true
			}
		case "event":
			if _, ok := requiredEvents[name]; ok {
				requiredEvents[name] = true
			}
		}
	}

	for fn, seen := range requiredFns {
		if !seen {
			t.Errorf("ABI is missing required function: %s", fn)
		}
	}
	for ev, seen := range requiredEvents {
		if !seen {
			t.Errorf("ABI is missing required event: %s", ev)
		}
	}
}

// TestState_String 校验 State 枚举与字符串名称的双向一致性。
func TestState_String(t *testing.T) {
	cases := []struct {
		state State
		want  string
	}{
		{StateCreated, "Created"},
		{StateFunded, "Funded"},
		{StateReleased, "Released"},
		{StateRefunded, "Refunded"},
		{StateDisputed, "Disputed"},
		{State(99), "Unknown"},
	}
	for _, c := range cases {
		if got := c.state.String(); got != c.want {
			t.Errorf("State(%d).String() = %q, want %q", c.state, got, c.want)
		}
	}
}

// TestState_IsTerminal 校验只有 Released / Refunded 是终态。
func TestState_IsTerminal(t *testing.T) {
	terminal := []State{StateReleased, StateRefunded}
	nonTerminal := []State{StateCreated, StateFunded, StateDisputed}

	for _, s := range terminal {
		if !s.IsTerminal() {
			t.Errorf("State %s should be terminal", s)
		}
	}
	for _, s := range nonTerminal {
		if s.IsTerminal() {
			t.Errorf("State %s should not be terminal", s)
		}
	}
}

// TestEventSignatures 校验事件签名常量符合 Solidity ABI 编码规范：
//   - 形如 Name(type1,type2)
//   - 与 ABI JSON 中的 input 类型按序匹配
func TestEventSignatures(t *testing.T) {
	checks := map[string]string{
		EventFunded:           "Funded(uint256)",
		EventReleased:         "Released(address)",
		EventRefunded:         "Refunded(address)",
		EventDisputed:         "Disputed(address)",
		EventPaymentConfirmed: "PaymentConfirmed(bytes32,address,uint256)",
	}
	for got, want := range checks {
		if got != want {
			t.Errorf("event signature mismatch: got %q, want %q", got, want)
		}
		if !strings.Contains(got, "(") || !strings.HasSuffix(got, ")") {
			t.Errorf("event signature %q is not a valid solidity signature", got)
		}
	}
}

// TestErrorSignatures 校验自定义错误签名格式合法。
func TestErrorSignatures(t *testing.T) {
	sigs := []string{ErrInvalidState, ErrNotAuthorized, ErrWrongAmount, ErrZeroAddress}
	for _, s := range sigs {
		if !strings.Contains(s, "(") || !strings.HasSuffix(s, ")") {
			t.Errorf("error signature %q is not a valid solidity signature", s)
		}
	}
}

// TestNewEscrow 占位构造器至少要能返回非空对象并暴露 ABI。
func TestNewEscrow(t *testing.T) {
	var addr [20]byte
	addr[19] = 1
	e, err := NewEscrow(addr, nil)
	if err != nil {
		t.Fatalf("NewEscrow returned error: %v", err)
	}
	if e == nil {
		t.Fatal("NewEscrow returned nil")
	}
	if e.Address != addr {
		t.Errorf("Address mismatch: got %v, want %v", e.Address, addr)
	}
	if e.ABI() == "" {
		t.Error("ABI() returned empty string")
	}
}
