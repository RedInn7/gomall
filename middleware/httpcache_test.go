package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func newCacheRouter(maxAge time.Duration, body string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/product/show", HTTPCache(maxAge), func(c *gin.Context) {
		c.Header("Content-Type", "application/json; charset=utf-8")
		_, _ = c.Writer.WriteString(body)
	})
	return r
}

func TestHTTPCache_FirstRequestSetsETagAndCacheControl(t *testing.T) {
	r := newCacheRouter(60*time.Second, `{"id":1,"name":"book"}`)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/product/show", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("want 200 got %d", w.Code)
	}
	etag := w.Header().Get("ETag")
	if etag == "" {
		t.Fatalf("expect ETag header to be set")
	}
	if !strings.HasPrefix(etag, `W/"`) {
		t.Fatalf("expect weak ETag prefix, got %q", etag)
	}
	cc := w.Header().Get("Cache-Control")
	if cc == "" {
		t.Fatalf("expect Cache-Control header to be set")
	}
	if !strings.Contains(cc, "max-age=60") {
		t.Fatalf("expect max-age=60, got %q", cc)
	}
	if !strings.Contains(cc, "public") {
		t.Fatalf("expect public directive, got %q", cc)
	}
	if w.Body.String() != `{"id":1,"name":"book"}` {
		t.Fatalf("body mismatch: %q", w.Body.String())
	}
}

func TestHTTPCache_IfNoneMatchReturns304(t *testing.T) {
	r := newCacheRouter(60*time.Second, `{"id":1,"name":"book"}`)

	w1 := httptest.NewRecorder()
	r.ServeHTTP(w1, httptest.NewRequest(http.MethodGet, "/product/show", nil))
	etag := w1.Header().Get("ETag")
	if etag == "" {
		t.Fatalf("first response missing ETag")
	}

	req := httptest.NewRequest(http.MethodGet, "/product/show", nil)
	req.Header.Set("If-None-Match", etag)
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req)

	if w2.Code != http.StatusNotModified {
		t.Fatalf("want 304 got %d body=%s", w2.Code, w2.Body.String())
	}
	if w2.Body.Len() != 0 {
		t.Fatalf("304 must not include body, got %q", w2.Body.String())
	}
	if w2.Header().Get("ETag") != etag {
		t.Fatalf("304 ETag should echo original, got %q want %q", w2.Header().Get("ETag"), etag)
	}
	if w2.Header().Get("Cache-Control") == "" {
		t.Fatalf("304 should still carry Cache-Control")
	}
}

func TestHTTPCache_StaleIfNoneMatchStillReturnsBody(t *testing.T) {
	r := newCacheRouter(60*time.Second, `{"id":1,"name":"book"}`)

	req := httptest.NewRequest(http.MethodGet, "/product/show", nil)
	req.Header.Set("If-None-Match", `W/"deadbeef"`)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("stale ETag should fall through to 200, got %d", w.Code)
	}
	if w.Body.String() == "" {
		t.Fatalf("body should be returned when ETag doesn't match")
	}
}

func TestHTTPCache_OnlyGETAndHEAD(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/x", HTTPCache(60*time.Second), func(c *gin.Context) {
		_, _ = c.Writer.WriteString(`{"ok":true}`)
	})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/x", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("want 200 got %d", w.Code)
	}
	if w.Header().Get("ETag") != "" {
		t.Fatalf("POST should not get ETag header")
	}
	if w.Header().Get("Cache-Control") != "" {
		t.Fatalf("POST should not get Cache-Control header")
	}
}

func TestHTTPCache_NonOKResponsesNotCached(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/missing", HTTPCache(60*time.Second), func(c *gin.Context) {
		c.JSON(http.StatusNotFound, gin.H{"err": "not found"})
	})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/missing", nil))

	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404 got %d", w.Code)
	}
	if w.Header().Get("ETag") != "" {
		t.Fatalf("404 should not get ETag header")
	}
}
