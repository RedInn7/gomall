// Package escrow 提供 contracts/Escrow.sol 的 Go 端绑定占位实现。
//
// 当前仓库默认不引入 github.com/ethereum/go-ethereum（避免一个 100MB+ 的依赖
// 进入业务模块），所以本文件只导出以下两类内容：
//
//  1. 协议常量：EscrowABI、State*、事件签名、错误签名
//  2. 接口契约：EscrowCaller / EscrowTransactor / EscrowFilterer，描述真正
//     的 abigen 产物应当满足的形状，供后续接 EVM listener 时按此接口实现
//
// 当 web3 模块正式上线、需要真正调用合约时，按 contracts/README.md 的命令
// 重新生成本文件即可覆盖。
//
//go:generate abigen --abi escrow.abi.json --pkg escrow --type Escrow --out escrow.gen.go
package escrow

import (
	_ "embed"
	"errors"
	"math/big"
)

// EscrowABI 是合约 ABI 的 JSON 字符串，由 escrow.abi.json 嵌入。
// abigen 在生成 binding 时会用同样的 ABI；此处保留是为了让仅依赖 ABI 的
// 链下消费者（EVM listener、log 解析器等）能直接拿到。
//
//go:embed escrow.abi.json
var EscrowABI string

// State 合约状态机的枚举，与 Solidity 端 enum State 一一对应。
type State uint8

const (
	StateCreated  State = 0
	StateFunded   State = 1
	StateReleased State = 2
	StateRefunded State = 3
	StateDisputed State = 4
)

// String 返回状态的可读名称，便于日志 / 监控展示。
func (s State) String() string {
	switch s {
	case StateCreated:
		return "Created"
	case StateFunded:
		return "Funded"
	case StateReleased:
		return "Released"
	case StateRefunded:
		return "Refunded"
	case StateDisputed:
		return "Disputed"
	default:
		return "Unknown"
	}
}

// IsTerminal 返回该状态是否为终态（不会再发生状态迁移）。
func (s State) IsTerminal() bool {
	return s == StateReleased || s == StateRefunded
}

// 事件签名常量。链下 listener 用这些字符串计算 topic[0] = keccak256(sig)。
const (
	EventFunded           = "Funded(uint256)"
	EventReleased         = "Released(address)"
	EventRefunded         = "Refunded(address)"
	EventDisputed         = "Disputed(address)"
	EventPaymentConfirmed = "PaymentConfirmed(bytes32,address,uint256)"
)

// 自定义错误签名常量。EVM revert data 的前 4 字节 = keccak256(sig)[:4]。
const (
	ErrInvalidState  = "InvalidState(uint8,uint8)"
	ErrNotAuthorized = "NotAuthorized(address)"
	ErrWrongAmount   = "WrongAmount(uint256,uint256)"
	ErrZeroAddress   = "ZeroAddress()"
)

// PaymentConfirmedEvent 是链下解析 PaymentConfirmed 事件后的强类型表示。
// 后端 listener 监听这个事件 -> 按 OrderID 查订单 -> 推进订单状态到 Paid。
type PaymentConfirmedEvent struct {
	OrderID [32]byte
	Buyer   [20]byte
	Amount  *big.Int
	TxHash  [32]byte
	Block   uint64
}

// ErrBindingNotGenerated 表示本文件仍是占位实现，真正调用前需要按
// contracts/README.md 的 abigen 命令重新生成 escrow.gen.go。
var ErrBindingNotGenerated = errors.New("escrow binding is a placeholder; run go generate to produce escrow.gen.go")

// EscrowCaller 是只读 view / pure 方法的接口契约。
//
// abigen 产物会用 *bind.BoundContract 实现这些方法；占位文件仅声明形状。
type EscrowCaller interface {
	Buyer() ([20]byte, error)
	Seller() ([20]byte, error)
	Arbiter() ([20]byte, error)
	Amount() (*big.Int, error)
	State() (State, error)
	OrderID() ([32]byte, error)
}

// EscrowTransactor 是会发交易的写方法接口契约。
//
// 实际实现需要 *bind.TransactOpts（签名者、gas、nonce），这里用 TxOpts 占位。
type EscrowTransactor interface {
	Fund(opts TxOpts) (TxResult, error)
	FundWithOrderID(opts TxOpts, orderID [32]byte) (TxResult, error)
	Release(opts TxOpts) (TxResult, error)
	Refund(opts TxOpts) (TxResult, error)
	Dispute(opts TxOpts) (TxResult, error)
}

// EscrowFilterer 描述事件订阅/过滤接口契约。
//
// WatchPaymentConfirmed 在真正绑定里对应：
//
//	WatchPaymentConfirmed(opts *bind.WatchOpts, sink chan<- *EscrowPaymentConfirmed,
//	    orderID [][32]byte, buyer []common.Address) (event.Subscription, error)
type EscrowFilterer interface {
	WatchPaymentConfirmed(opts WatchOpts, sink chan<- *PaymentConfirmedEvent, orderIDFilter ...[32]byte) (Subscription, error)
	FilterPaymentConfirmed(opts FilterOpts, orderIDFilter ...[32]byte) ([]*PaymentConfirmedEvent, error)
}

// TxOpts 是发送交易时的可调参数；真正实现里对应 bind.TransactOpts。
type TxOpts struct {
	From     [20]byte
	Value    *big.Int
	GasLimit uint64
	GasPrice *big.Int
	Nonce    *big.Int
}

// WatchOpts 对应 bind.WatchOpts。
type WatchOpts struct {
	Start   *uint64
	Context any
}

// FilterOpts 对应 bind.FilterOpts。
type FilterOpts struct {
	Start uint64
	End   *uint64
}

// TxResult 是发交易的占位返回；真正绑定返回 *types.Transaction。
type TxResult struct {
	Hash [32]byte
}

// Subscription 是事件订阅的占位接口；真正绑定返回 event.Subscription。
type Subscription interface {
	Unsubscribe()
	Err() <-chan error
}

// Escrow 是合约入口的占位类型。
//
// 真正绑定里它会持有 *bind.BoundContract 并实现 EscrowCaller /
// EscrowTransactor / EscrowFilterer 三套接口。
type Escrow struct {
	Address [20]byte
	abi     string
}

// NewEscrow 构造占位的 Escrow 句柄；真正实现里它会接 *bind.BoundContract。
//
// backend 形参是为了与 abigen 产物的签名 NewEscrow(address common.Address,
// backend bind.ContractBackend) 保持一致；占位实现忽略它。
func NewEscrow(address [20]byte, backend any) (*Escrow, error) {
	_ = backend
	return &Escrow{Address: address, abi: EscrowABI}, nil
}

// ABI 暴露已嵌入的 ABI JSON，便于上层做日志解析或 ABI 校验。
func (e *Escrow) ABI() string {
	return e.abi
}
