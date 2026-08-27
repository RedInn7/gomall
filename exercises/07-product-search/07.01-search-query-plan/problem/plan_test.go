//go:build exercise

package searchqueryplan

import (
	"errors"
	"reflect"
	"testing"
)

func boolPtr(v bool) *bool { return &v }

func TestInfoHasHighestKeywordPriority(t *testing.T) {
	plan, err := BuildSearchPlan(SearchRequest{Info: "露营", Title: "咖啡壶", Name: "手冲壶", PageNum: 1, PageSize: 10})
	if err != nil || plan.Keyword != "露营" {
		t.Fatalf("plan=%+v err=%v", plan, err)
	}
}

func TestTitleIsUsedWhenInfoIsBlank(t *testing.T) {
	plan, err := BuildSearchPlan(SearchRequest{Info: "  ", Title: " 咖啡壶 ", Name: "手冲壶"})
	if err != nil || plan.Keyword != "咖啡壶" {
		t.Fatalf("plan=%+v err=%v", plan, err)
	}
}

func TestNameIsFinalKeywordFallback(t *testing.T) {
	plan, err := BuildSearchPlan(SearchRequest{Name: "  手冲壶  "})
	if err != nil || plan.Keyword != "手冲壶" {
		t.Fatalf("plan=%+v err=%v", plan, err)
	}
}

func TestEmptyKeywordIsAllowed(t *testing.T) {
	plan, err := BuildSearchPlan(SearchRequest{})
	if err != nil || plan.Keyword != "" {
		t.Fatalf("plan=%+v err=%v", plan, err)
	}
}

func TestInvalidPageUsesDefaults(t *testing.T) {
	plan, err := BuildSearchPlan(SearchRequest{PageNum: -3, PageSize: 0})
	if err != nil || plan.From != 0 || plan.Size != DefaultPageSize {
		t.Fatalf("plan=%+v err=%v", plan, err)
	}
}

func TestPageSizeIsCappedBeforeOffsetCalculation(t *testing.T) {
	plan, err := BuildSearchPlan(SearchRequest{PageNum: 3, PageSize: 1000})
	if err != nil || plan.From != 200 || plan.Size != MaxPageSize {
		t.Fatalf("plan=%+v err=%v", plan, err)
	}
}

func TestOffsetUsesNormalizedPageAndSize(t *testing.T) {
	plan, err := BuildSearchPlan(SearchRequest{PageNum: 4, PageSize: 25})
	if err != nil || plan.From != 75 || plan.Size != 25 {
		t.Fatalf("plan=%+v err=%v", plan, err)
	}
}

func TestCategoryBecomesFilter(t *testing.T) {
	plan, err := BuildSearchPlan(SearchRequest{CategoryID: 7})
	want := []Filter{{Field: "category_id", Value: "7"}}
	if err != nil || !reflect.DeepEqual(plan.Filters, want) {
		t.Fatalf("filters=%+v err=%v", plan.Filters, err)
	}
}

func TestOnSaleTrueBecomesFilter(t *testing.T) {
	plan, err := BuildSearchPlan(SearchRequest{OnSale: boolPtr(true)})
	want := []Filter{{Field: "on_sale", Value: "true"}}
	if err != nil || !reflect.DeepEqual(plan.Filters, want) {
		t.Fatalf("filters=%+v err=%v", plan.Filters, err)
	}
}

func TestOnSaleFalseIsNotMistakenForMissing(t *testing.T) {
	plan, err := BuildSearchPlan(SearchRequest{OnSale: boolPtr(false)})
	want := []Filter{{Field: "on_sale", Value: "false"}}
	if err != nil || !reflect.DeepEqual(plan.Filters, want) {
		t.Fatalf("filters=%+v err=%v", plan.Filters, err)
	}
}

func TestFiltersHaveStableOrder(t *testing.T) {
	plan, err := BuildSearchPlan(SearchRequest{CategoryID: 9, OnSale: boolPtr(true)})
	want := []Filter{{Field: "category_id", Value: "9"}, {Field: "on_sale", Value: "true"}}
	if err != nil || !reflect.DeepEqual(plan.Filters, want) {
		t.Fatalf("filters=%+v err=%v", plan.Filters, err)
	}
}

func TestNegativeCategoryReturnsNoPartialPlan(t *testing.T) {
	plan, err := BuildSearchPlan(SearchRequest{Info: "咖啡", CategoryID: -1, PageNum: 2, PageSize: 10})
	if !errors.Is(err, ErrInvalidCategory) {
		t.Fatalf("err=%v", err)
	}
	if !reflect.DeepEqual(plan, QueryPlan{}) {
		t.Fatalf("partial plan=%+v", plan)
	}
}
