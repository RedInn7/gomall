package main

import (
	"fmt"
	"github.com/RedInn7/gomall/internal/product"
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

func main() {
	db, err := gorm.Open(mysql.Open(DSN), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		panic("链接数据库失败，err: " + err.Error())
	}
	err = db.AutoMigrate(&product.Product{})
	if err != nil {
		panic("链接数据库失败，err: " + err.Error())
	}
	fmt.Printf("🚀 开始生成 %d 条数据...\n", TotalRecords)
	startTime := time.Now()
	var buffer []product.Product
	count := 0
	for i := 0; i < TotalRecords; i++ {
		priceVal := rand.Float64() * 1000
		discountVal := priceVal * 0.8
		p := product.Product{
			Name:          fmt.Sprintf("高性能商品_%d", i),
			CategoryID:    uint(rand.Intn(50) + 1), // 假设有 50 个分类
			Title:         fmt.Sprintf("这是第 %d 个商品的超长标题，用于测试数据库读取性能", i),
			Info:          "这里是一段很长的商品详情描述，用于占用数据库页空间，模拟真实的 IO 压力...",
			ImgPath:       fmt.Sprintf("https://static.mall.com/img/%d.jpg", rand.Intn(10000)),
			Price:         fmt.Sprintf("%.2f", priceVal),
			DiscountPrice: fmt.Sprintf("%.2f", discountVal),
			OnSale:        rand.Intn(2) == 1, // 50% 概率上架
			Num:           rand.Intn(10000),  // 库存
			BossID:        uint(rand.Intn(100) + 1),
			BossName:      fmt.Sprintf("商家_%d", rand.Intn(100)),
			BossAvatar:    "avatar.jpg",
		}
		buffer = append(buffer, p)
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
