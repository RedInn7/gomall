package milvus

import (
	"context"
	"os"

	"github.com/milvus-io/milvus-sdk-go/v2/client"
)

// MilvusClient 全局 Milvus 客户端，未配置 MILVUS_ADDR 时保持 nil
var MilvusClient client.Client

// InitMilvus 按 MILVUS_ADDR 建立连接；未配置或连接失败时返回 nil/err 由上层决定
// 是否阻塞启动。约定：addr 为空 -> 静默不启动（不返回 error）
func InitMilvus() error {
	addr := os.Getenv("MILVUS_ADDR")
	if addr == "" {
		return nil
	}
	c, err := client.NewClient(context.Background(), client.Config{Address: addr})
	if err != nil {
		return err
	}
	MilvusClient = c
	return nil
}
