//go:build !cgo

package carousel

import (
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// CGO 关闭时 mattn 驱动只剩 stub，无法注册自定义函数；
// 返回默认 Dialector，gorm.Open 会失败并触发整组 skip。
func newTestDialector(dsn string) gorm.Dialector {
	return sqlite.Open(dsn)
}
