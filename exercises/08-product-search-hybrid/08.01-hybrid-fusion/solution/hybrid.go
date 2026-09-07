//go:build exercise

package hybridfusion

import (
	"errors"
	"sort"
)

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

func FuseTopK(keywordHits []KeywordHit, keywordErr error, vectorHits []VectorHit, vectorErr error, topK int) ([]Result, error) {
	if topK <= 0 {
		return nil, ErrInvalidTopK
	}
	if keywordErr != nil && vectorErr != nil {
		return nil, ErrBothRecallsFailed
	}

	var keyword map[uint]float64
	if keywordErr == nil {
		keyword = bestKeywordScores(keywordHits)
	}
	var semantic map[uint]float64
	if vectorErr == nil {
		semantic = bestVectorDistances(vectorHits)
	}

	keywordNorm := normalizeScores(keyword)
	semanticNorm := normalizeDistances(semantic)
	keywordActive := len(keywordNorm) > 0
	semanticActive := len(semanticNorm) > 0

	fused := make(map[uint]Result, len(keywordNorm)+len(semanticNorm))
	for id, score := range keywordNorm {
		r := fused[id]
		r.ProductID = id
		r.KeywordScore = score
		fused[id] = r
	}
	for id, score := range semanticNorm {
		r := fused[id]
		r.ProductID = id
		r.SemanticScore = score
		fused[id] = r
	}

	results := make([]Result, 0, len(fused))
	for _, r := range fused {
		switch {
		case keywordActive && semanticActive:
			r.Score = 0.5*r.KeywordScore + 0.5*r.SemanticScore
		case keywordActive:
			r.Score = r.KeywordScore
		case semanticActive:
			r.Score = r.SemanticScore
		}
		results = append(results, r)
	}
	sort.Slice(results, func(i, j int) bool {
		if results[i].Score == results[j].Score {
			return results[i].ProductID < results[j].ProductID
		}
		return results[i].Score > results[j].Score
	})
	if len(results) > topK {
		results = results[:topK]
	}
	return results, nil
}

func bestKeywordScores(hits []KeywordHit) map[uint]float64 {
	best := make(map[uint]float64, len(hits))
	for _, hit := range hits {
		if score, ok := best[hit.ProductID]; !ok || hit.Score > score {
			best[hit.ProductID] = hit.Score
		}
	}
	return best
}

func bestVectorDistances(hits []VectorHit) map[uint]float64 {
	best := make(map[uint]float64, len(hits))
	for _, hit := range hits {
		if distance, ok := best[hit.ProductID]; !ok || hit.Distance < distance {
			best[hit.ProductID] = hit.Distance
		}
	}
	return best
}

func normalizeScores(values map[uint]float64) map[uint]float64 {
	if len(values) == 0 {
		return map[uint]float64{}
	}
	min, max := bounds(values)
	normalized := make(map[uint]float64, len(values))
	for id, value := range values {
		if min == max {
			normalized[id] = 1
		} else {
			normalized[id] = (value - min) / (max - min)
		}
	}
	return normalized
}

func normalizeDistances(values map[uint]float64) map[uint]float64 {
	if len(values) == 0 {
		return map[uint]float64{}
	}
	min, max := bounds(values)
	normalized := make(map[uint]float64, len(values))
	for id, value := range values {
		if min == max {
			normalized[id] = 1
		} else {
			normalized[id] = (max - value) / (max - min)
		}
	}
	return normalized
}

func bounds(values map[uint]float64) (float64, float64) {
	first := true
	var min, max float64
	for _, value := range values {
		if first || value < min {
			min = value
		}
		if first || value > max {
			max = value
		}
		first = false
	}
	return min, max
}
