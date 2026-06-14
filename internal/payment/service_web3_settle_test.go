package payment

import (
	"errors"
	"testing"
)

// USDC 稳定币 1:1：$100(10000 分) → 100 USDC(6 位精度 = 100000000 base units)。
func TestVerifyOnchainAmount_USDC(t *testing.T) {
	t.Setenv(envWeb3PayToken, "usdc")
	t.Setenv(envWeb3USDCDecimals, "6")
	t.Setenv(envWeb3ToleranceBps, "50")

	const payable = int64(10000) // $100
	// 足额 / 超付通过
	if err := verifyOnchainAmount(payable, "100000000"); err != nil {
		t.Fatalf("足额 USDC 应通过, got %v", err)
	}
	if err := verifyOnchainAmount(payable, "100500000"); err != nil {
		t.Fatalf("超付 USDC 应通过, got %v", err)
	}
	// 明显少付被拒
	if err := verifyOnchainAmount(payable, "99000000"); !errors.Is(err, ErrWeb3AmountMismatch) {
		t.Fatalf("少付 USDC 应 ErrWeb3AmountMismatch, got %v", err)
	}
}

// ETH 需喂价：未配置喂价应报错；配置后按 wei 校验。
func TestVerifyOnchainAmount_ETH(t *testing.T) {
	t.Setenv(envWeb3PayToken, "eth")
	t.Setenv(envWeb3ToleranceBps, "50")

	// 未配喂价
	t.Setenv(envWeb3CentsPerETH, "")
	if err := verifyOnchainAmount(10000, "1"); !errors.Is(err, ErrWeb3PriceNotConfigured) {
		t.Fatalf("ETH 未配喂价应 ErrWeb3PriceNotConfigured, got %v", err)
	}

	// $3000/ETH，$100 → 1e22/3e5 = 33333333333333333 wei
	t.Setenv(envWeb3CentsPerETH, "300000")
	if err := verifyOnchainAmount(10000, "33333333333333333"); err != nil {
		t.Fatalf("足额 ETH 应通过, got %v", err)
	}
	if err := verifyOnchainAmount(10000, "10000000000000000"); !errors.Is(err, ErrWeb3AmountMismatch) {
		t.Fatalf("少付 ETH 应 ErrWeb3AmountMismatch, got %v", err)
	}
}

// 合约 bytes32 hex 还原 gomall 订单 id。
func TestDecodeOrderIDFromBytes32(t *testing.T) {
	cases := map[string]uint{
		"0x000000000000000000000000000000000000000000000000000000000000007b": 123,
		"0x7b": 123,
		"7b":   123,
	}
	for in, want := range cases {
		got, err := decodeOrderIDFromBytes32(in)
		if err != nil || got != want {
			t.Fatalf("decode %q = %d,%v; want %d", in, got, err, want)
		}
	}
	if _, err := decodeOrderIDFromBytes32("0x0"); err == nil {
		t.Fatalf("零 order id 应报错")
	}
	if _, err := decodeOrderIDFromBytes32("zz"); err == nil {
		t.Fatalf("非法 hex 应报错")
	}
}
