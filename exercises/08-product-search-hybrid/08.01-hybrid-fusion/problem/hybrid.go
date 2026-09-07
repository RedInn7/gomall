//go:build exercise

package hybridfusion

import "errors"

var (
	ErrInvalidTopK       = errors.New("topK must be positive")
	ErrBothRecallsFailed = errors.New("both recall routes failed")
)

type KeywordHit struct {
	ProductID uint
	Score     float64
}

type VectorHit struct {
	ProductID uint
	Distance  float64
}

type Result struct {
	ProductID     uint
	KeywordScore  float64
	SemanticScore float64
	Score         float64
}

// FuseTopK normalizes, merges, ranks, and truncates the two recall routes.
func FuseTopK(keywordHits []KeywordHit, keywordErr error, vectorHits []VectorHit, vectorErr error, topK int) ([]Result, error) {
	// TODO: 完成 Hybrid Search 融合：
	// 1. 校验 topK；两路同时失败时返回 ErrBothRecallsFailed。
	// 2. 失败链路的数据不得参与计算；健康链路先按 ProductID 去重。
	// 3. 关键词 Score 做 min-max 归一化；向量 Distance 做反向归一化。
	//    单值或全相等时，该路所有归一化得分为 1。
	// 4. 按 ProductID 合并。两路都有候选时各占 0.5；只有一路有候选时
	//    直接使用该路得分。
	// 5. 最终分数降序、ProductID 升序排序，并返回不超过 topK 条。
	// 不要修改调用方传入的切片。
	return nil, nil
}
