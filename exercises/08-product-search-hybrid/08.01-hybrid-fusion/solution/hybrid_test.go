//go:build exercise

package hybridfusion

import (
	"errors"
	"math"
	"reflect"
	"testing"
)

func TestFuseCombinesSameProductFromBothRoutes(t *testing.T) {
	got, err := FuseTopK(
		[]KeywordHit{{ProductID: 1, Score: 20}, {ProductID: 2, Score: 10}}, nil,
		[]VectorHit{{ProductID: 1, Distance: 0.2}, {ProductID: 3, Distance: 0.8}}, nil, 3,
	)
	if err != nil {
		t.Fatal(err)
	}
	want := []Result{
		{ProductID: 1, KeywordScore: 1, SemanticScore: 1, Score: 1},
		{ProductID: 2, KeywordScore: 0, SemanticScore: 0, Score: 0},
		{ProductID: 3, KeywordScore: 0, SemanticScore: 0, Score: 0},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got=%+v want=%+v", got, want)
	}
}

func TestTopKTruncatesAfterStableRanking(t *testing.T) {
	got, err := FuseTopK(
		[]KeywordHit{{ProductID: 3, Score: 10}, {ProductID: 1, Score: 10}, {ProductID: 2, Score: 10}}, nil,
		nil, nil, 2,
	)
	if err != nil {
		t.Fatal(err)
	}
	if ids := resultIDs(got); !reflect.DeepEqual(ids, []uint{1, 2}) {
		t.Fatalf("TopK IDs=%v", ids)
	}
}

func TestTopKLargerThanCandidatesReturnsAll(t *testing.T) {
	got, err := FuseTopK([]KeywordHit{{ProductID: 8, Score: 4}}, nil, nil, nil, 20)
	if err != nil || len(got) != 1 || got[0].ProductID != 8 {
		t.Fatalf("got=%+v err=%v", got, err)
	}
}

func TestInvalidTopKReturnsNoPartialResults(t *testing.T) {
	got, err := FuseTopK([]KeywordHit{{ProductID: 1, Score: 1}}, nil, nil, nil, 0)
	if !errors.Is(err, ErrInvalidTopK) || got != nil {
		t.Fatalf("got=%+v err=%v", got, err)
	}
}

func TestKeywordOnlyUsesFullNormalizedScore(t *testing.T) {
	got, err := FuseTopK(
		[]KeywordHit{{ProductID: 1, Score: 30}, {ProductID: 2, Score: 20}, {ProductID: 3, Score: 10}}, nil,
		nil, nil, 3,
	)
	if err != nil {
		t.Fatal(err)
	}
	wantScores := []float64{1, 0.5, 0}
	if len(got) != len(wantScores) {
		t.Fatalf("got %d results, want %d: %+v", len(got), len(wantScores), got)
	}
	for i, want := range wantScores {
		if got[i].Score != want || got[i].KeywordScore != want {
			t.Fatalf("result[%d]=%+v want score=%v", i, got[i], want)
		}
	}
}

func TestVectorDistanceIsNormalizedInReverse(t *testing.T) {
	got, err := FuseTopK(nil, nil, []VectorHit{
		{ProductID: 1, Distance: 0.9},
		{ProductID: 2, Distance: 0.1},
		{ProductID: 3, Distance: 0.5},
	}, nil, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d results, want 3: %+v", len(got), got)
	}
	if ids := resultIDs(got); !reflect.DeepEqual(ids, []uint{2, 3, 1}) {
		t.Fatalf("distance ranking IDs=%v", ids)
	}
	if got[0].Score != 1 || got[1].Score != 0.5 || got[2].Score != 0 {
		t.Fatalf("scores=%+v", got)
	}
}

func TestKeywordFailureFallsBackToVector(t *testing.T) {
	got, err := FuseTopK(
		[]KeywordHit{{ProductID: 99, Score: 1000}}, errors.New("ES unavailable"),
		[]VectorHit{{ProductID: 2, Distance: 0.1}, {ProductID: 3, Distance: 0.4}}, nil, 5,
	)
	if err != nil || !reflect.DeepEqual(resultIDs(got), []uint{2, 3}) {
		t.Fatalf("got=%+v err=%v", got, err)
	}
}

func TestVectorFailureFallsBackToKeyword(t *testing.T) {
	got, err := FuseTopK(
		[]KeywordHit{{ProductID: 4, Score: 2}, {ProductID: 5, Score: 1}}, nil,
		[]VectorHit{{ProductID: 99, Distance: 0}}, errors.New("Milvus unavailable"), 5,
	)
	if err != nil || !reflect.DeepEqual(resultIDs(got), []uint{4, 5}) {
		t.Fatalf("got=%+v err=%v", got, err)
	}
}

func TestBothRecallFailuresReturnDefinedError(t *testing.T) {
	got, err := FuseTopK(nil, errors.New("ES unavailable"), nil, errors.New("Milvus unavailable"), 5)
	if !errors.Is(err, ErrBothRecallsFailed) || got != nil {
		t.Fatalf("got=%+v err=%v", got, err)
	}
}

func TestEqualValuesDoNotProduceNaN(t *testing.T) {
	got, err := FuseTopK(
		[]KeywordHit{{ProductID: 2, Score: 7}, {ProductID: 1, Score: 7}}, nil,
		[]VectorHit{{ProductID: 2, Distance: 0.4}, {ProductID: 1, Distance: 0.4}}, nil, 2,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(resultIDs(got), []uint{1, 2}) {
		t.Fatalf("stable IDs=%v", resultIDs(got))
	}
	for _, result := range got {
		if math.IsNaN(result.Score) || result.Score != 1 {
			t.Fatalf("result=%+v", result)
		}
	}
}

func TestDuplicateHitsKeepBestValuePerRoute(t *testing.T) {
	got, err := FuseTopK(
		[]KeywordHit{{ProductID: 1, Score: 2}, {ProductID: 1, Score: 9}, {ProductID: 2, Score: 5}}, nil,
		[]VectorHit{{ProductID: 1, Distance: 0.8}, {ProductID: 1, Distance: 0.1}, {ProductID: 2, Distance: 0.4}}, nil, 2,
	)
	if err != nil || len(got) != 2 {
		t.Fatalf("got=%+v err=%v", got, err)
	}
	if got[0] != (Result{ProductID: 1, KeywordScore: 1, SemanticScore: 1, Score: 1}) {
		t.Fatalf("best duplicate not kept: %+v", got)
	}
}

func TestSuccessfulEmptyRouteDoesNotHalveWorkingRoute(t *testing.T) {
	got, err := FuseTopK(nil, nil, []VectorHit{{ProductID: 7, Distance: 0.3}}, nil, 1)
	if err != nil || len(got) != 1 || got[0].Score != 1 || got[0].SemanticScore != 1 {
		t.Fatalf("got=%+v err=%v", got, err)
	}
}

func TestEmptySuccessfulRoutesReturnEmptyResult(t *testing.T) {
	got, err := FuseTopK(nil, nil, nil, nil, 10)
	if err != nil || len(got) != 0 {
		t.Fatalf("got=%+v err=%v", got, err)
	}
}

func TestInputsAreNotMutated(t *testing.T) {
	keyword := []KeywordHit{{ProductID: 2, Score: 1}, {ProductID: 1, Score: 2}}
	vector := []VectorHit{{ProductID: 2, Distance: 0.1}, {ProductID: 1, Distance: 0.2}}
	keywordBefore := append([]KeywordHit(nil), keyword...)
	vectorBefore := append([]VectorHit(nil), vector...)
	if _, err := FuseTopK(keyword, nil, vector, nil, 2); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(keyword, keywordBefore) || !reflect.DeepEqual(vector, vectorBefore) {
		t.Fatalf("inputs mutated: keyword=%+v vector=%+v", keyword, vector)
	}
}

func resultIDs(results []Result) []uint {
	ids := make([]uint, len(results))
	for i, result := range results {
		ids[i] = result.ProductID
	}
	return ids
}
