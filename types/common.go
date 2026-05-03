package types

import "github.com/RedInn7/gomall/consts"

type BasePage struct {
	PageNum  int `form:"page_num"`
	PageSize int `form:"page_size"`
	LastId   int `form:"last_id"`
}

func (p *BasePage) Normalize() {
	if p == nil {
		return
	}
	if p.PageNum < 1 {
		p.PageNum = 1
	}
	if p.PageSize <= 0 {
		p.PageSize = consts.BasePageSize
	}
	if p.PageSize > consts.MaxPageSize {
		p.PageSize = consts.MaxPageSize
	}
	if p.LastId < 0 {
		p.LastId = 0
	}
}

// DataListResp 带有总数的Data结构
type DataListResp struct {
	Item  interface{} `json:"item"`
	Total int64       `json:"total"`
}
