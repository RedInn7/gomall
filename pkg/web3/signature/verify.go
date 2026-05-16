// Package signature 提供 EIP-191 personal_sign 风格的钱包签名校验。
//
// 服务端持有 (addr, msg, sig)，需要确认 sig 确实由 addr 对应的私钥签出，
// 且签名内容就是 msg —— 即典型的链下身份证明 / 授权确认场景。
//
// 该实现遵循以太坊 personal_sign 约定：
//   1. 原始消息前置 "\x19Ethereum Signed Message:\n<len>" 前缀；
//   2. 对前缀+消息做 Keccak256 得到 hash；
//   3. 由 65 字节 sig 还原 pubkey，再推导 address；
//   4. 与传入 addr 做大小写无关比较。
//
// 这样可以避免钱包对任意 32 字节 hash 直接签名，从而规避钓鱼者
// 把一段恶意 calldata 伪装成可签名 hash 的攻击面。
package signature

import (
	"bytes"
	"errors"
	"fmt"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// 65 = 32(r) + 32(s) + 1(v)，标准 secp256k1 序列化长度
const personalSigLen = 65

// VerifyPersonalSign 校验 sig 是否由 addr 对 msg 用 EIP-191 personal_sign 签出。
// addr 必须是 0x 前缀的十六进制地址，比较时忽略大小写。
func VerifyPersonalSign(addr string, msg []byte, sig []byte) (bool, error) {
	if len(sig) != personalSigLen {
		return false, fmt.Errorf("signature 长度异常: got %d, want %d", len(sig), personalSigLen)
	}
	if !common.IsHexAddress(addr) {
		return false, errors.New("钱包地址格式非法")
	}

	// 拷贝一份，避免污染调用方传入的 slice
	rsv := make([]byte, personalSigLen)
	copy(rsv, sig)

	// 钱包侧 v 一般是 27/28，go-ethereum 还原 pubkey 需要 0/1
	switch rsv[64] {
	case 27, 28:
		rsv[64] -= 27
	case 0, 1:
		// already normalized
	default:
		return false, fmt.Errorf("非法 v 值: %d", rsv[64])
	}

	hash := personalSignHash(msg)
	pubKey, err := crypto.SigToPub(hash, rsv)
	if err != nil {
		return false, fmt.Errorf("还原 pubkey 失败: %w", err)
	}

	recovered := crypto.PubkeyToAddress(*pubKey)
	expected := common.HexToAddress(addr)
	// common.Address 内部就是 [20]byte，bytes.Equal 比较即可，
	// 但走 HexToAddress 已自带 checksum 规整，确保大小写不敏感
	if !bytes.Equal(recovered.Bytes(), expected.Bytes()) {
		return false, nil
	}
	return true, nil
}

// personalSignHash 实现 EIP-191:
//   keccak256("\x19Ethereum Signed Message:\n" + len(msg) + msg)
func personalSignHash(msg []byte) []byte {
	prefix := fmt.Sprintf("\x19Ethereum Signed Message:\n%d", len(msg))
	buf := make([]byte, 0, len(prefix)+len(msg))
	buf = append(buf, []byte(prefix)...)
	buf = append(buf, msg...)
	return crypto.Keccak256(buf)
}

// NormalizeAddress 把地址统一成小写 0x 前缀，方便 Redis key / 业务比对。
func NormalizeAddress(addr string) string {
	return strings.ToLower(common.HexToAddress(addr).Hex())
}
