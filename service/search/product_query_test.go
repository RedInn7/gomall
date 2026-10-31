package search

import (
	"context"
	"errors"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	elastic "github.com/elastic/go-elasticsearch"
	"github.com/sirupsen/logrus"

	"github.com/RedInn7/gomall/internal/product"
	"github.com/RedInn7/gomall/pkg/e"
	util "github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/repository/es"
	"github.com/RedInn7/gomall/types"
)

// installFakeES 让 es.EsClient 指向固定返回 status/body 的 httptest 服务，返回请求计数
func installFakeES(t *testing.T, status int, body string) *atomic.Int32 {
	t.Helper()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	client, err := elastic.NewClient(elastic.Config{Addresses: []string{server.URL}})
	if err != nil {
		t.Fatal(err)
	}
	previous := es.EsClient
	es.EsClient = client
	t.Cleanup(func() { es.EsClient = previous })
	return &calls
}

// stubDBSearch 记录每次 DB 回退收到的关键词，返回空结果
func stubDBSearch(t *testing.T) *[]string {
	t.Helper()
	logger := logrus.New()
	logger.Out = io.Discard
	previousLog, previousDB := util.LogrusObj, dbSearch
	util.LogrusObj = logger
	var keywords []string
	dbSearch = func(_ context.Context, keyword string, _ types.BasePage) ([]*product.Product, int64, error) {
		keywords = append(keywords, keyword)
		return nil, 0, nil
	}
	t.Cleanup(func() { util.LogrusObj, dbSearch = previousLog, previousDB })
	return &keywords
}

func TestProductSearchRejectsPagesBeyondResultWindow(t *testing.T) {
	calls := installFakeES(t, http.StatusOK, `{"hits":{"total":{"value":0},"hits":[]}}`)
	dbKeywords := stubDBSearch(t)

	for _, pageNum := range []int{101, math.MaxInt} {
		req := &product.ProductSearchReq{Name: "咖啡壶", BasePage: types.BasePage{PageNum: pageNum, PageSize: 100}}
		_, err := ProductSearch(context.Background(), req)
		if e.CodeOf(err) != e.InvalidParams {
			t.Fatalf("page_num=%d want InvalidParams, got %v", pageNum, err)
		}
	}
	if calls.Load() != 0 || len(*dbKeywords) != 0 {
		t.Fatalf("deep pages must not reach ES or DB: es=%d db=%v", calls.Load(), *dbKeywords)
	}

	// 恰好 from+size=10000 仍然允许
	req := &product.ProductSearchReq{Name: "咖啡壶", BasePage: types.BasePage{PageNum: 100, PageSize: 100}}
	if _, err := ProductSearch(context.Background(), req); err != nil || calls.Load() != 1 {
		t.Fatalf("last page inside the window must hit ES: err=%v calls=%d", err, calls.Load())
	}
}

func TestProductSearchReturnsES4xxWithoutDBFallback(t *testing.T) {
	installFakeES(t, http.StatusBadRequest, `{"error":{"type":"illegal_argument_exception"},"status":400}`)
	dbKeywords := stubDBSearch(t)

	_, err := ProductSearch(context.Background(), &product.ProductSearchReq{Name: "咖啡壶"})
	if !errors.Is(err, es.ErrBadRequest) {
		t.Fatalf("want ES 4xx surfaced, got %v", err)
	}
	if len(*dbKeywords) != 0 {
		t.Fatalf("4xx must not fall back to DB, got %v", *dbKeywords)
	}
}

func TestProductSearchFallsBackOn5xxWithTheSameKeyword(t *testing.T) {
	installFakeES(t, http.StatusServiceUnavailable, `{"error":"unavailable","status":503}`)
	dbKeywords := stubDBSearch(t)

	if _, err := ProductSearch(context.Background(), &product.ProductSearchReq{Title: "防水跑鞋"}); err != nil {
		t.Fatal(err)
	}
	if len(*dbKeywords) != 1 || (*dbKeywords)[0] != "防水跑鞋" {
		t.Fatalf("DB fallback must search the same keyword as ES, got %v", *dbKeywords)
	}
}
