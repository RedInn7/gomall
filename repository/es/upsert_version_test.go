package es

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"

	elastic "github.com/elastic/go-elasticsearch"
)

func TestUpsertProductWritesExternalVersionAndIgnoresStaleConflict(t *testing.T) {
	var query url.Values
	status := http.StatusCreated
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(server.Close)
	client, err := elastic.NewClient(elastic.Config{Addresses: []string{server.URL}})
	if err != nil {
		t.Fatal(err)
	}
	previous := EsClient
	EsClient = client
	t.Cleanup(func() { EsClient = previous })

	updatedAt := time.Date(2026, 10, 31, 8, 0, 0, 123_000_000, time.UTC)
	p := &ProductDoc{ID: 12, Name: "phone", Version: updatedAt.UnixNano()}
	if err := UpsertProduct(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if query.Get("version_type") != "external_gte" || query.Get("version") != strconv.FormatInt(p.Version, 10) {
		t.Fatalf("upsert must carry updated_at as external version, got %v", query)
	}

	// 回填拿着旧快照晚到：ES 拒绝并返回 409，说明索引里已是更新的数据
	status = http.StatusConflict
	if err := UpsertProduct(context.Background(), p); err != nil {
		t.Fatalf("stale write rejected by ES must count as done: %v", err)
	}
}
