package search

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

const (
	embeddingDim     = 768
	embeddingTimeout = 5 * time.Second
	envEmbeddingURL  = "EMBEDDING_API_URL"
	envEmbeddingKey  = "EMBEDDING_API_KEY"
	envEmbeddingMdl  = "EMBEDDING_MODEL"
	defaultModel     = "text-embedding-3-small"
)

// embeddingRequest 与多数主流 embedding 服务 (OpenAI / 兼容网关) 字段一致
type embeddingRequest struct {
	Model string `json:"model"`
	Input string `json:"input"`
}

// embeddingResponse 兼容两种返回形态:
//   1. {"data":[{"embedding":[...]}]} — OpenAI 风格
//   2. {"embedding":[...]}            — 自建 BGE / Ollama 风格
type embeddingResponse struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
	Embedding []float32 `json:"embedding"`
}

// httpDoer 抽象出来便于测试覆盖
type httpDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

var embedHTTPClient httpDoer = &http.Client{Timeout: embeddingTimeout}

// EmbedText 把文本转向量。
//   - 未配置 EMBEDDING_API_URL：返回固定 dim=768 的占位向量 (SHA-256 衍生)，让上游接口能跑通
//   - 配置了：POST {model, input}，5s 超时
func EmbedText(ctx context.Context, text string) ([]float32, error) {
	url := os.Getenv(envEmbeddingURL)
	if url == "" {
		return stubEmbedding(text), nil
	}
	model := os.Getenv(envEmbeddingMdl)
	if model == "" {
		model = defaultModel
	}
	payload, err := json.Marshal(embeddingRequest{Model: model, Input: text})
	if err != nil {
		return nil, err
	}
	reqCtx, cancel := context.WithTimeout(ctx, embeddingTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if k := os.Getenv(envEmbeddingKey); k != "" {
		req.Header.Set("Authorization", "Bearer "+k)
	}
	resp, err := embedHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("embedding api status=%d body=%s", resp.StatusCode, string(body))
	}
	var parsed embeddingResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, err
	}
	if len(parsed.Embedding) > 0 {
		return parsed.Embedding, nil
	}
	if len(parsed.Data) > 0 && len(parsed.Data[0].Embedding) > 0 {
		return parsed.Data[0].Embedding, nil
	}
	return nil, errors.New("embedding api returned empty vector")
}

// stubEmbedding 用 SHA-256 衍生 768 维占位向量，保证同 text 得到同 vec，便于本地与测试
func stubEmbedding(text string) []float32 {
	vec := make([]float32, embeddingDim)
	seed := sha256.Sum256([]byte(text))
	// 32 字节 hash 反复填充，每 4 字节折成一个 float32 (归一到 [-1, 1])
	for i := 0; i < embeddingDim; i++ {
		offset := (i * 4) % len(seed)
		// 不够 4 字节就回卷
		buf := make([]byte, 4)
		for j := 0; j < 4; j++ {
			buf[j] = seed[(offset+j)%len(seed)]
		}
		u := binary.BigEndian.Uint32(buf)
		vec[i] = float32(int32(u))/float32(1<<31) - 0.0
	}
	return vec
}
