package main

import (
	"fmt"
	"time"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"

	"github.com/RedInn7/gomall/internal/order"
	"github.com/RedInn7/gomall/pkg/utils/log"
)

const (
	DSN           = "mall:123456@tcp(127.0.0.1:3306)/mall_db?charset=utf8mb4&parseTime=True&loc=Local"
	DeleteRecords = 1000000 // 要删除的数据量
	BatchSize     = 5000    // 每次删 5000 条 (安全水位，不会锁死 DB)
)

func main() {
	log.InitLog()
	db, err := gorm.Open(mysql.Open(DSN), &gorm.Config{
		Logger:         logger.Default.LogMode(logger.Silent),
		NamingStrategy: schema.NamingStrategy{SingularTable: true}, // 与服务一致，表名是 order
	})
	if err != nil {
		panic("链接数据库失败，err: " + err.Error())
	}
	var maxId uint
	err = db.Unscoped().Model(&order.Order{}).Select("max(id)").Scan(&maxId).Error
	if err != nil {
		panic("获取max id失败，err: " + err.Error())
	}

	startId := uint(0)
	if maxId > DeleteRecords {
		startId = maxId - DeleteRecords
	}
	startTime := time.Now()
	var totalDeleted int64
	fmt.Println("🚀 开始删除 1000000 条数据...")
	for i := startId; i < maxId; i += BatchSize {
		end := min(maxId+1, i+BatchSize)
		result := db.Unscoped().Where("id>=? and id<?", i, end).Delete(&order.Order{})
		if result.Error != nil {
			log.LogrusObj.Errorf("删除数据失败，err:%v\n，删除id范围：[%v,%v)\n", result.Error, i, end)
		}
		totalDeleted += result.RowsAffected
		fmt.Printf("删除进度 %v/%v\n", totalDeleted, DeleteRecords)
	}
	duration := time.Since(startTime)
	fmt.Printf("\n\n✅ 删除完成！\n实际删除行数: %d\n耗时: %v\nTPS: %.0f/s\n",
		totalDeleted, duration, float64(totalDeleted)/duration.Seconds())
}
