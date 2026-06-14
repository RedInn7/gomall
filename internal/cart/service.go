package cart

import (
	"context"
	"errors"
	"sync"

	"gorm.io/gorm"

	"github.com/RedInn7/gomall/internal/product"
	"github.com/RedInn7/gomall/pkg/e"
	"github.com/RedInn7/gomall/pkg/utils/ctl"
	util "github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/types"
)

var CartSrvIns *CartSrv
var CartSrvOnce sync.Once

type CartSrv struct {
}

func GetCartSrv() *CartSrv {
	CartSrvOnce.Do(func() {
		CartSrvIns = &CartSrv{}
	})
	return CartSrvIns
}

// CartCreate 创建购物车。成功时不回传数据（保持既有 API 契约：data 为 null）。
func (s *CartSrv) CartCreate(ctx context.Context, req *CartCreateReq) (*types.DataListResp, error) {
	u, err := ctl.GetUserInfo(ctx)
	if err != nil {
		util.LogrusObj.Error(err)
		return nil, err
	}
	// 判断有无这个商品
	_, err = product.NewProductDao(ctx).GetProductById(req.ProductId)
	if err != nil {
		util.LogrusObj.Error(err)
		return nil, err
	}

	// 创建购物车
	cartDao := NewCartDao(ctx)
	_, status, _ := cartDao.CreateCart(req.ProductId, u.Id, req.BossID)
	if status == e.ErrorProductMoreCart {
		return nil, errors.New(e.GetMsg(status))
	}
	return nil, nil
}

// CartList 购物车
func (s *CartSrv) CartList(ctx context.Context, req *CartListReq) (*types.DataListResp, error) {
	u, err := ctl.GetUserInfo(ctx)
	if err != nil {
		util.LogrusObj.Error(err)
		return nil, err
	}
	carts, err := NewCartDao(ctx).ListCartByUserId(u.Id)
	if err != nil {
		util.LogrusObj.Error(err)
		return nil, err
	}

	return &types.DataListResp{
		Item:  carts, // 列表暂不分页
		Total: int64(len(carts)),
	}, nil
}

// CartUpdate 修改购物车信息。成功时不回传数据（保持既有 API 契约：data 为 null）。
// 数量必须在 [1, cart.MaxNum] 范围内：0 留存无效空行，超上限绕过加购路径的封顶逻辑。
func (s *CartSrv) CartUpdate(ctx context.Context, req *UpdateCartServiceReq) (*types.DataListResp, error) {
	u, err := ctl.GetUserInfo(ctx)
	if err != nil {
		util.LogrusObj.Error(err)
		return nil, err
	}
	if req.Num < 1 {
		return nil, errors.New("数量不能小于 1")
	}
	cartDao := NewCartDao(ctx)
	cart, err := cartDao.GetCartByRowId(req.Id, u.Id)
	if err != nil {
		// 购物车不存在或不归属当前用户：静默返回，与 UpdateCartNumById 0 行命中行为一致
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		util.LogrusObj.Error(err)
		return nil, err
	}
	if req.Num > cart.MaxNum {
		return nil, errors.New(e.GetMsg(e.ErrorProductMoreCart))
	}
	if err = cartDao.UpdateCartNumById(req.Id, u.Id, req.Num); err != nil {
		util.LogrusObj.Error(err)
		return nil, err
	}

	return nil, nil
}

// CartDelete 删除购物车。成功时不回传数据（保持既有 API 契约：data 为 null）。
func (s *CartSrv) CartDelete(ctx context.Context, req *CartDeleteReq) (*types.DataListResp, error) {
	u, err := ctl.GetUserInfo(ctx)
	if err != nil {
		util.LogrusObj.Error(err)
		return nil, err
	}
	if err = NewCartDao(ctx).DeleteCartById(req.Id, u.Id); err != nil {
		util.LogrusObj.Error(err)
		return nil, err
	}

	return nil, nil
}
