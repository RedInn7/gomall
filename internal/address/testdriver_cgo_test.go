//go:build cgo

package address

import (
	"database/sql"
	"sync"
	"time"

	sqlite3 "github.com/mattn/go-sqlite3"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// repo 层 SELECT 里用了 MySQL 的 UNIX_TIMESTAMP()，sqlite 没有内置同名函数。
// 在测试驱动上按相同语义注册一个，让 repo 的 SQL 原样跑通。
const addressTestDriverName = "sqlite3_address_unixts"

var addressTestDriverOnce sync.Once

func newTestDialector(dsn string) gorm.Dialector {
	addressTestDriverOnce.Do(func() {
		sql.Register(addressTestDriverName, &sqlite3.SQLiteDriver{
			ConnectHook: func(conn *sqlite3.SQLiteConn) error {
				return conn.RegisterFunc("UNIX_TIMESTAMP", sqliteUnixTimestamp, true)
			},
		})
	})
	return &sqlite.Dialector{DriverName: addressTestDriverName, DSN: dsn}
}

// sqliteUnixTimestamp 解析 sqlite 驱动落盘的 datetime 文本，返回 Unix 秒。
func sqliteUnixTimestamp(ts string) int64 {
	layouts := []string{
		"2006-01-02 15:04:05.999999999-07:00",
		"2006-01-02 15:04:05-07:00",
		time.RFC3339Nano,
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, ts); err == nil {
			return t.Unix()
		}
	}
	return 0
}
