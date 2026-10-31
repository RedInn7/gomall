package milvus

import (
	"context"
	"os"
	"time"

	"github.com/milvus-io/milvus-sdk-go/v2/client"
)

// MilvusClient 全局 Milvus 客户端，未配置 MILVUS_ADDR 时保持 nil
var MilvusClient client.Client

// dialTimeout SDK 默认带 grpc.WithBlock()，不设超时的话 Milvus 未就绪会让启动一直卡住
var dialTimeout = 5 * time.Second

// InitMilvus 按 MILVUS_ADDR 建立连接；未配置或连接失败时返回 nil/err 由上层决定
// 是否阻塞启动。约定：addr 为空 -> 静默不启动（不返回 error）；连接超时按不可用返回 err
func InitMilvus() error {
	addr := os.Getenv("MILVUS_ADDR")
	if addr == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), dialTimeout)
	defer cancel()
	c, err := client.NewClient(ctx, client.Config{Address: addr})
	if err != nil {
		return err
	}
	MilvusClient = c
	return nil
}
