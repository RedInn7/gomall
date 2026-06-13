package category

import (
	"context"
	"sync"

	util "github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/types"
)

var CategorySrvIns *CategorySrv
var CategorySrvOnce sync.Once

type CategorySrv struct {
}

func GetCategorySrv() *CategorySrv {
	CategorySrvOnce.Do(func() {
		CategorySrvIns = &CategorySrv{}
	})
	return CategorySrvIns
}

// CategoryList 列举分类
func (s *CategorySrv) CategoryList(ctx context.Context, req *ListCategoryReq) (*types.DataListResp, error) {
	categories, err := NewCategoryDao(ctx).ListCategory()
	if err != nil {
		util.LogrusObj.Error(err)
		return nil, err
	}
	cResp := make([]*ListCategoryResp, 0)
	for _, v := range categories {
		cResp = append(cResp, &ListCategoryResp{
			ID:           v.ID,
			CategoryName: v.CategoryName,
			CreatedAt:    v.CreatedAt.Unix(),
		})
	}

	return &types.DataListResp{
		Item:  cResp,
		Total: int64(len(cResp)),
	}, nil
}
