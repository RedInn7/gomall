//go:build exercise

package searchqueryplan

import "errors"

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

// BuildSearchPlan converts the public request into the deterministic contract
// consumed by the Elasticsearch adapter.
func BuildSearchPlan(req SearchRequest) (QueryPlan, error) {
	// TODO: 完成搜索请求清洗与查询计划构造：
	// 1. 对 Info、Title、Name 去除首尾空白，并按 info > title > name
	//    选择第一个非空关键词；三个字段都为空时允许 Keyword 为空。
	// 2. PageNum 小于 1 时按第 1 页处理；PageSize 不大于 0 时使用 20，
	//    大于 100 时限制为 100；From = (pageNum-1)*pageSize。
	// 3. CategoryID 小于 0 时返回 ErrInvalidCategory，且不返回半成品计划；
	//    大于 0 时追加 category_id filter。
	// 4. OnSale 非 nil 时追加 on_sale filter，并保留 true/false 的真实值；
	//    nil 表示调用方没有提供该条件。
	// 5. Filters 顺序固定为 category_id、on_sale，方便缓存和测试稳定。
	return QueryPlan{}, nil
}
