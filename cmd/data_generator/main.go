package main

import (
	"fmt"
	snowflake "github.com/RedInn7/gomall/pkg/utils/snowflake"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"math/rand"
	"time"
)

const (
	DSN          = "mall:123456@tcp(127.0.0.1:3306)/mall_db?charset=utf8mb4&parseTime=True&loc=Local"
	TotalRecords = 1000000 // 目标：100万
	BatchSize    = 2000    // 批次大小：每次插 2000 条 (MySQL 的最佳性能区间)
)

type Order struct {
	gorm.Model         // 自动包含 ID, CreatedAt, UpdatedAt, DeletedAt
	OrderNum   uint64  `gorm:"index"` // 订单号 (业务唯一键)
	UserID     uint    `gorm:"index"` // 用户ID (加索引，因为常用 user_id 查询)
	ProductID  uint    `gorm:"index"` // 商品ID
	BossID     uint    `gorm:"index"` // 商家ID
	AddressID  uint    `gorm:"index"` // 地址ID
	Num        int     // 购买数量
	Money      float64 // 金额 (建议用 int 存“分”，或者 decimal，这里为了演示用 float)
	Type       int     `gorm:"type:tinyint;index"` // 订单状态 (待支付、已支付等)
}

func (Order) TableName() string {
	return "order"
}

func main() {
	db, err := gorm.Open(mysql.Open(DSN), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		panic("链接数据库失败，err: " + err.Error())
	}
	err = db.AutoMigrate(&Order{})
	if err != nil {
		panic("链接数据库失败，err: " + err.Error())
	}
	fmt.Printf("🚀 开始生成 %d 条数据...\n", TotalRecords)
	startTime := time.Now()
	var buffer []Order
	count := 0
	snowflake.InitSnowflake(1)
	for i := 0; i < TotalRecords; i++ {
		order := Order{
			OrderNum:  uint64(snowflake.GenSnowflakeID()),
			UserID:    uint(rand.Intn(50000)),
			ProductID: uint(rand.Intn(200)),
			BossID:    uint(rand.Intn(4000)),
			AddressID: uint(rand.Intn(2000)),
			Num:       rand.Intn(2000),
			Money:     float64(rand.Intn(10000)) / 100.0,
			Type:      rand.Intn(5),
		}
		buffer = append(buffer, order)
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
