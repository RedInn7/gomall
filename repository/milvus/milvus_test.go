package milvus

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/milvus-io/milvus-sdk-go/v2/client"
	"github.com/milvus-io/milvus-sdk-go/v2/entity"
)

func TestInitMilvusGivesUpWhenServerNeverAnswers(t *testing.T) {
	// 端口能连上但从不回 gRPC 握手，模拟 Milvus 还在启动
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		var conns []net.Conn
		defer func() {
			for _, c := range conns {
				_ = c.Close()
			}
		}()
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			conns = append(conns, conn)
		}
	}()
	t.Setenv("MILVUS_ADDR", ln.Addr().String())
	previous := dialTimeout
	dialTimeout = 200 * time.Millisecond
	t.Cleanup(func() { dialTimeout = previous })

	start := time.Now()
	if err := InitMilvus(); err == nil {
		t.Fatal("unreachable Milvus must return an error")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("InitMilvus blocked for %s", elapsed)
	}
	if MilvusClient != nil {
		t.Fatal("client must stay nil when the dial fails")
	}
}

// hangingLoadClient 集合和索引都已就绪，但 Load 一直不返回
type hangingLoadClient struct{ client.Client }

func (hangingLoadClient) HasCollection(context.Context, string) (bool, error) { return true, nil }

func (hangingLoadClient) DescribeIndex(context.Context, string, string, ...client.IndexOption) ([]entity.Index, error) {
	idx, err := entity.NewIndexHNSW(entity.L2, hnswM, hnswEfConstruction)
	return []entity.Index{idx}, err
}

func (hangingLoadClient) LoadCollection(ctx context.Context, _ string, _ bool, _ ...client.LoadCollectionOption) error {
	<-ctx.Done()
	return ctx.Err()
}

func TestEnsureProductVectorCollectionGivesUpWhenLoadHangs(t *testing.T) {
	previousClient, previousTimeout := MilvusClient, ensureTimeout
	MilvusClient, ensureTimeout = hangingLoadClient{}, 100*time.Millisecond
	t.Cleanup(func() { MilvusClient, ensureTimeout = previousClient, previousTimeout })

	start := time.Now()
	if err := EnsureProductVectorCollection(context.Background()); err == nil {
		t.Fatal("hanging load must return an error")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("EnsureProductVectorCollection blocked for %s", elapsed)
	}
}
