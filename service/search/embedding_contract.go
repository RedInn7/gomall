package search

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
)

const (
	envEmbeddingProviderID  = "EMBEDDING_PROVIDER_ID"
	envEmbeddingTextVersion = "EMBEDDING_TEXT_VERSION"
	defaultTextVersion      = "product-v1"
)

// EmbeddingContract 标识一套不能混用的商品向量。
type EmbeddingContract struct {
	ProviderID  string
	Model       string
	Dimensions  int
	TextVersion string
	Metric      string
}

func CurrentEmbeddingContract() EmbeddingContract {
	provider := strings.TrimSpace(os.Getenv(envEmbeddingProviderID))
	model := strings.TrimSpace(os.Getenv(envEmbeddingMdl))
	if strings.TrimSpace(os.Getenv(envEmbeddingURL)) == "" {
		provider = "local"
		model = "sha256-stub-v1"
	} else {
		if provider == "" {
			provider = "custom"
		}
		if model == "" {
			model = defaultModel
		}
	}
	textVersion := strings.TrimSpace(os.Getenv(envEmbeddingTextVersion))
	if textVersion == "" {
		textVersion = defaultTextVersion
	}
	return EmbeddingContract{ProviderID: provider, Model: model, Dimensions: embeddingDim, TextVersion: textVersion, Metric: "L2"}
}

func (c EmbeddingContract) Fingerprint() string {
	canonical := fmt.Sprintf("provider=%s\nmodel=%s\ndim=%d\ntext=%s\nmetric=%s", c.ProviderID, c.Model, c.Dimensions, c.TextVersion, c.Metric)
	sum := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(sum[:8])
}

func (c EmbeddingContract) CollectionName() string {
	return "product_vector_" + c.Fingerprint()
}
