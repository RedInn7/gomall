package favorite

import (
	"context"
	"errors"
	"strings"
	"sync"

	conf "github.com/RedInn7/gomall/config"
	"github.com/RedInn7/gomall/consts"
	"github.com/RedInn7/gomall/internal/product"
	"github.com/RedInn7/gomall/internal/user"
	"github.com/RedInn7/gomall/pkg/utils/ctl"
	util "github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/types"
)

var FavoriteSrvIns *FavoriteSrv
var FavoriteSrvOnce sync.Once

type FavoriteSrv struct {
}

func GetFavoriteSrv() *FavoriteSrv {
	FavoriteSrvOnce.Do(func() {
		FavoriteSrvIns = &FavoriteSrv{}
	})
	return FavoriteSrvIns
}

// FavoriteList 商品收藏夹
func (s *FavoriteSrv) FavoriteList(ctx context.Context, req *FavoritesServiceReq) (*types.DataListResp, error) {
	u, err := ctl.GetUserInfo(ctx)
	if err != nil {
		util.LogrusObj.Error(err)
		return nil, err
	}
	favorites, total, err := NewFavoritesDao(ctx).ListFavoriteByUserId(u.Id, req.PageSize, req.PageNum)
	if err != nil {
		util.LogrusObj.Error(err)
		return nil, err
	}
	for i := range favorites {
		if conf.Config.System.UploadModel == consts.UploadModelLocal {
			favorites[i].ImgPath = conf.Config.PhotoPath.PhotoHost + conf.Config.System.HttpPort + conf.Config.PhotoPath.ProductPath + favorites[i].ImgPath
		}
	}

	return &types.DataListResp{
		Item:  favorites,
		Total: total,
	}, nil
}

// FavoriteCreate 创建收藏夹
func (s *FavoriteSrv) FavoriteCreate(ctx context.Context, req *FavoriteCreateReq) (*Favorite, error) {
	u, err := ctl.GetUserInfo(ctx)
	if err != nil {
		util.LogrusObj.Error(err)
		return nil, err
	}
	fDao := NewFavoritesDao(ctx)
	exist, err := fDao.FavoriteExistOrNot(u.Id, req.ProductId)
	if err != nil {
		util.LogrusObj.Error(err)
		return nil, err
	}
	if exist {
		err = errors.New("已经存在了")
		util.LogrusObj.Error(err)
		return nil, err
	}

	userDao := user.NewUserDao(ctx)
	curUser, err := userDao.GetUserById(u.Id)
	util.LogrusObj.Infof("user: %+v\n", curUser)
	if err != nil {
		util.LogrusObj.Error(err)
		return nil, err
	}

	// 先查商品：收藏夹里的卖家以商品表为准，忽略 req.BossId。虽然收藏当前不参与计费，
	// 但存错的 boss 会污染下游（卖家维度统计 / 后续加购下单），按同一信任边界规则收口。
	prod, err := product.NewProductDao(ctx).GetProductById(req.ProductId)
	if err != nil {
		util.LogrusObj.Error(err)
		return nil, err
	}

	bossDao := user.NewUserDaoByDB(userDao.DB)
	boss, err := bossDao.GetUserById(prod.BossID)
	if err != nil {
		util.LogrusObj.Error(err)
		return nil, err
	}

	favorite := &Favorite{
		UserID:    u.Id,
		User:      *curUser,
		ProductID: req.ProductId,
		Product:   *prod,
		BossID:    prod.BossID,
		Boss:      *boss,
	}
	err = fDao.CreateFavorite(favorite)
	if err != nil {
		// 并发场景下，唯一索引冲突视为重复收藏，非服务端异常
		if isDuplicateEntryError(err) {
			return nil, errors.New("已经存在了")
		}
		util.LogrusObj.Error(err)
		return nil, err
	}

	return nil, nil
}

// isDuplicateEntryError 判断错误是否由唯一索引冲突引起（MySQL / SQLite 兼容）。
func isDuplicateEntryError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	// MySQL: "Error 1062: Duplicate entry ..."
	// SQLite: "UNIQUE constraint failed: ..."
	return strings.Contains(msg, "1062") ||
		strings.Contains(msg, "Duplicate entry") ||
		strings.Contains(msg, "UNIQUE constraint failed")
}

// FavoriteDelete 删除收藏夹，仅允许操作当前登录用户自己的收藏
func (s *FavoriteSrv) FavoriteDelete(ctx context.Context, req *FavoriteDeleteReq) (*Favorite, error) {
	if req.ProductId == 0 {
		return nil, errors.New("product_id 不能为空")
	}
	u, err := ctl.GetUserInfo(ctx)
	if err != nil {
		util.LogrusObj.Error(err)
		return nil, err
	}
	favoriteDao := NewFavoritesDao(ctx)
	var exist bool
	exist, err = favoriteDao.FavoriteExistOrNot(u.Id, req.ProductId)
	if err != nil {
		util.LogrusObj.Error(err)
		return nil, err
	}
	if !exist {
		return nil, errors.New("不存在对应收藏夹")
	}
	err = favoriteDao.DeleteFavoriteByUserIdAndProductId(u.Id, req.ProductId)
	if err != nil {
		util.LogrusObj.Error(err)
		return nil, err
	}

	return nil, nil
}
