//go:build exercise

package searchqueryplan

import (
	"errors"
	"strconv"
	"strings"
)

const (
	DefaultPageSize = 20
	MaxPageSize     = 100
)

var ErrInvalidCategory = errors.New("category_id must not be negative")

type SearchRequest struct {
	Info       string
	Title      string
	Name       string
	CategoryID int
	OnSale     *bool
	PageNum    int
	PageSize   int
}

type Filter struct {
	Field string
	Value string
}

type QueryPlan struct {
	Keyword string
	From    int
	Size    int
	Filters []Filter
}

func BuildSearchPlan(req SearchRequest) (QueryPlan, error) {
	if req.CategoryID < 0 {
		return QueryPlan{}, ErrInvalidCategory
	}

	keyword := ""
	for _, candidate := range []string{req.Info, req.Title, req.Name} {
		if trimmed := strings.TrimSpace(candidate); trimmed != "" {
			keyword = trimmed
			break
		}
	}

	pageNum := req.PageNum
	if pageNum < 1 {
		pageNum = 1
	}
	pageSize := req.PageSize
	if pageSize <= 0 {
		pageSize = DefaultPageSize
	} else if pageSize > MaxPageSize {
		pageSize = MaxPageSize
	}

	plan := QueryPlan{
		Keyword: keyword,
		From:    (pageNum - 1) * pageSize,
		Size:    pageSize,
	}
	if req.CategoryID > 0 {
		plan.Filters = append(plan.Filters, Filter{Field: "category_id", Value: strconv.Itoa(req.CategoryID)})
	}
	if req.OnSale != nil {
		plan.Filters = append(plan.Filters, Filter{Field: "on_sale", Value: strconv.FormatBool(*req.OnSale)})
	}
	return plan, nil
}
