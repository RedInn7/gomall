package signature

import (
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/crypto"
)

// signPersonal 用一把测试私钥按 EIP-191 规范对 msg 签名，返回 65 字节 sig (v=27/28)。
func signPersonal(t *testing.T, msg []byte) (addr string, sig []byte) {
	t.Helper()
	priv, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	hash := personalSignHash(msg)
	rsv, err := crypto.Sign(hash, priv)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	// crypto.Sign 返回的 v 是 0/1，回放到钱包风格的 27/28
	rsv[64] += 27
	return crypto.PubkeyToAddress(priv.PublicKey).Hex(), rsv
}

func TestVerifyPersonalSign_OK(t *testing.T) {
	msg := []byte("gomall:paydown:order=1024:nonce=abcdef:chain=1")
	addr, sig := signPersonal(t, msg)

	ok, err := VerifyPersonalSign(addr, msg, sig)
	if err != nil {
		t.Fatalf("verify err: %v", err)
	}
	if !ok {
		t.Fatalf("expected verify ok, got false")
	}

	// 大小写不敏感
	ok, err = VerifyPersonalSign(strings.ToLower(addr), msg, sig)
	if err != nil || !ok {
		t.Fatalf("lowercase addr should verify: ok=%v err=%v", ok, err)
	}
}

func TestVerifyPersonalSign_WrongAddr(t *testing.T) {
	msg := []byte("gomall:paydown:order=1024:nonce=abcdef:chain=1")
	_, sig := signPersonal(t, msg)

	// 另一把私钥的地址，肯定不匹配
	other, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("gen other key: %v", err)
	}
	otherAddr := crypto.PubkeyToAddress(other.PublicKey).Hex()

	ok, err := VerifyPersonalSign(otherAddr, msg, sig)
	if err != nil {
		t.Fatalf("verify err: %v", err)
	}
	if ok {
		t.Fatal("expected verify false for wrong addr")
	}
}

func TestVerifyPersonalSign_TamperedMessage(t *testing.T) {
	msg := []byte("gomall:paydown:order=1024:nonce=abcdef:chain=1")
	addr, sig := signPersonal(t, msg)

	tampered := []byte("gomall:paydown:order=9999:nonce=abcdef:chain=1")
	ok, err := VerifyPersonalSign(addr, tampered, sig)
	if err != nil {
		t.Fatalf("verify err: %v", err)
	}
	if ok {
		t.Fatal("tampered message must not verify")
	}
}

func TestVerifyPersonalSign_BadSigLength(t *testing.T) {
	_, err := VerifyPersonalSign("0x0000000000000000000000000000000000000000", []byte("x"), []byte{0x01, 0x02})
	if err == nil {
		t.Fatal("expected length error")
	}
}

func TestVerifyPersonalSign_BadAddress(t *testing.T) {
	_, sig := signPersonal(t, []byte("hello"))
	_, err := VerifyPersonalSign("not-an-addr", []byte("hello"), sig)
	if err == nil {
		t.Fatal("expected invalid address error")
	}
}

func TestNormalizeAddress(t *testing.T) {
	mixed := "0xAbCDef0123456789ABCDef0123456789aBcDEF01"
	got := NormalizeAddress(mixed)
	if got != strings.ToLower(mixed) {
		t.Fatalf("normalize mismatch: got %s", got)
	}
}
