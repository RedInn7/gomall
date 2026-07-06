package e

import "errors"

// Error 是"带业务码的错误"：把错误码焊进 error 值本身。
//
// 之前各领域各写一套 error→业务码 的映射（preorder 私有 codedError、
// groupbuy 的 errors.Is switch、circuitbreaker 手写 gin.H），同一件事三种写法。
// 收敛成这一个类型后：领域层 return e.New(code)，统一出口用 CodeOf 提码透出，
// 加新错误只需在 code.go / msg.go 挂一个码，无需再改任何映射。
type Error struct {
	Code int
	Msg  string
}

func (err *Error) Error() string { return err.Msg }

// New 按错误码构造带码 error，文案取自 MsgFlags。
func New(code int) *Error {
	return &Error{Code: code, Msg: GetMsg(code)}
}

// CodeOf 沿 error 链提取业务码；不是带码 error（含裸 errors.New）时返回 ERROR。
func CodeOf(err error) int {
	if err == nil {
		return SUCCESS
	}
	var ce *Error
	if errors.As(err, &ce) {
		return ce.Code
	}
	return ERROR
}
