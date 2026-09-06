package search

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEmbedTextRequestsAndValidatesConfiguredDimensions(t *testing.T) {
	var dimensions int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Dimensions int `json:"dimensions"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		dimensions = body.Dimensions
		_ = json.NewEncoder(w).Encode(map[string]any{"embedding": []float32{1, 2, 3}})
	}))
	t.Cleanup(server.Close)
	t.Setenv(envEmbeddingURL, server.URL)

	_, err := EmbedText(context.Background(), "phone")
	if dimensions != embeddingDim || err == nil {
		t.Fatalf("embedding contract not enforced: dimensions=%d err=%v", dimensions, err)
	}
}

func TestValidateEmbeddingRejectsNonFiniteValues(t *testing.T) {
	vec := make([]float32, embeddingDim)
	vec[7] = float32(math.NaN())
	if err := validateEmbedding(vec, embeddingDim); err == nil {
		t.Fatal("NaN embedding must be rejected")
	}
}

func TestEmbeddingContractChangesCollectionAcrossModelsAndTextVersions(t *testing.T) {
	base := EmbeddingContract{ProviderID: "provider-a", Model: "model-v1", Dimensions: embeddingDim, TextVersion: "product-v1", Metric: "L2"}
	changedModel := base
	changedModel.Model = "model-v2"
	changedText := base
	changedText.TextVersion = "product-v2"
	if base.CollectionName() == changedModel.CollectionName() || base.CollectionName() == changedText.CollectionName() {
		t.Fatal("different embedding contracts must not share a Milvus collection")
	}
	if base.CollectionName() != base.CollectionName() {
		t.Fatal("embedding contract fingerprint must be deterministic")
	}
}

func TestEmbeddingContractSeparatesDifferentEndpointsWithoutLeakingSecrets(t *testing.T) {
	t.Setenv(envEmbeddingProviderID, "")
	t.Setenv(envEmbeddingMdl, "same-model")
	t.Setenv(envEmbeddingURL, "https://provider-a.example/v1/embeddings?api_key=secret-a")
	first := CurrentEmbeddingContract()
	t.Setenv(envEmbeddingURL, "https://provider-b.example/v1/embeddings?api_key=secret-b")
	second := CurrentEmbeddingContract()
	if first.CollectionName() == second.CollectionName() {
		t.Fatal("different embedding endpoints must not share a collection")
	}
	if strings.Contains(first.ProviderID, "secret-a") || strings.Contains(second.ProviderID, "secret-b") {
		t.Fatal("endpoint identity must not retain query secrets")
	}
}
