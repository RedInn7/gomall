package main

import (
	"fmt"
	"math/rand"
	"time"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"

	"github.com/RedInn7/gomall/consts"
	"github.com/RedInn7/gomall/internal/order"
	snowflake "github.com/RedInn7/gomall/pkg/utils/snowflake"
)

const (
	DSN          = "mall:123456@tcp(127.0.0.1:3306)/mall_db?charset=utf8mb4&parseTime=True&loc=Local"
	TotalRecords = 1000000 // 目标：100万
	BatchSize    = 2000    // 批次大小：每次插 2000 条 (MySQL 的最佳性能区间)
)

// 只生成不会被定时任务接管的状态：待付款会被超时关单并释放库存，
// 待收货会被自动确认收货并触发结算，这两种假数据会污染库存和资金。
var orderTypes = []uint{consts.OrderWaitShip, consts.OrderClosed, consts.OrderCompleted}

func main() {
	db, err := gorm.Open(mysql.Open(DSN), &gorm.Config{
		Logger:         logger.Default.LogMode(logger.Silent),
		NamingStrategy: schema.NamingStrategy{SingularTable: true}, // 与服务一致，表名是 order
	})
	if err != nil {
		panic("链接数据库失败，err: " + err.Error())
	}
	// 表结构以服务启动时的 AutoMigrate 为准，这里只灌数据，不改表
	if !db.Migrator().HasTable(&order.Order{}) {
		panic("order 表不存在，请先启动一次服务完成迁移")
	}
	fmt.Printf("🚀 开始生成 %d 条数据...\n", TotalRecords)
	startTime := time.Now()
	var buffer []order.Order
	count := 0
	snowflake.InitSnowflake(1)
	for i := 0; i < TotalRecords; i++ {
		o := order.Order{
			OrderNum:  uint64(snowflake.GenSnowflakeID()),
			UserID:    5,
			ProductID: uint(rand.Intn(2)),
			BossID:    uint(rand.Intn(4000)),
			AddressID: uint(rand.Intn(2000)),
			Num:       rand.Intn(2000) + 1,
			Money:     int64(rand.Intn(10000)), // 单位：分
			Type:      orderTypes[rand.Intn(len(orderTypes))],
		}
		buffer = append(buffer, o)
		if len(buffer) >= BatchSize {
			if err := db.CreateInBatches(buffer, len(buffer)).Error; err != nil {
				panic("批量创建错误，err:" + err.Error())
			}
			count += len(buffer)
			buffer = buffer[:0]
			fmt.Printf("\r进度: %d / %d", count, TotalRecords)
		}
	}
	if len(buffer) > 0 {
		if err := db.CreateInBatches(buffer, len(buffer)).Error; err != nil {
			panic("批量创建错误，err:" + err.Error())
		}
	}
	duration := time.Since(startTime)
	fmt.Printf("\n\n✅ 完成！\n耗时: %v\nTPS: %.0f/s\n", duration, float64(TotalRecords)/duration.Seconds())
}
