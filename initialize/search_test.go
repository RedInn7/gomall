package initialize

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	elastic "github.com/elastic/go-elasticsearch"
	"github.com/sirupsen/logrus"

	util "github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/repository/es"
)

func TestInitSearchKeepsRetryingUntilESIsReachable(t *testing.T) {
	logger := logrus.New()
	logger.Out = io.Discard
	previousLog := util.LogrusObj
	util.LogrusObj = logger
	t.Cleanup(func() { util.LogrusObj = previousLog })

	// 前两次请求模拟 ES 还没起来，之后索引已存在
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) <= 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)
	client, err := elastic.NewClient(elastic.Config{Addresses: []string{server.URL}})
	if err != nil {
		t.Fatal(err)
	}
	previousES := es.EsClient
	es.EsClient = client
	t.Cleanup(func() { es.EsClient = previousES })

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	InitSearch(ctx)

	// 第一次：HEAD 503 → 建索引 503 → 失败；1 秒后第二次 HEAD 200 → 成功
	deadline := time.Now().Add(5 * time.Second)
	for calls.Load() < 3 {
		if time.Now().After(deadline) {
			t.Fatalf("InitSearch gave up after ES was unreachable at startup, calls=%d", calls.Load())
		}
		time.Sleep(10 * time.Millisecond)
	}
}
