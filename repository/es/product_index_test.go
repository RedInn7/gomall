package es

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	elastic "github.com/elastic/go-elasticsearch"
)

func TestSearchProductsWithScoreFiltersOffSaleProductsAtRecall(t *testing.T) {
	var requestBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
			t.Error(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"hits":{"total":{"value":0},"hits":[]}}`))
	}))
	t.Cleanup(server.Close)
	client, err := elastic.NewClient(elastic.Config{Addresses: []string{server.URL}})
	if err != nil {
		t.Fatal(err)
	}
	previous := EsClient
	EsClient = client
	t.Cleanup(func() { EsClient = previous })

	if _, _, err := SearchProductsWithScore(context.Background(), "phone", 0, 10, nil); err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(requestBody)
	if !containsJSONTerm(requestBody, "on_sale", true) {
		t.Fatalf("hybrid ES recall did not filter on_sale=true: %s", encoded)
	}
}

func containsJSONTerm(value any, key string, want any) bool {
	switch current := value.(type) {
	case map[string]any:
		if got, ok := current[key]; ok && got == want {
			return true
		}
		for _, child := range current {
			if containsJSONTerm(child, key, want) {
				return true
			}
		}
	case []any:
		for _, child := range current {
			if containsJSONTerm(child, key, want) {
				return true
			}
		}
	}
	return false
}
