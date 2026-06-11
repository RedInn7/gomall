package favorite

import (
	"context"
	"errors"
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
func (s *FavoriteSrv) FavoriteList(ctx context.Context, req *FavoritesServiceReq) (resp interface{}, err error) {
	u, err := ctl.GetUserInfo(ctx)
	if err != nil {
		util.LogrusObj.Error(err)
		return nil, err
	}
	favorites, total, err := NewFavoritesDao(ctx).ListFavoriteByUserId(u.Id, req.PageSize, req.PageNum)
	if err != nil {
		util.LogrusObj.Error(err)
		return
	}
	for i := range favorites {
		if conf.Config.System.UploadModel == consts.UploadModelLocal {
			favorites[i].ImgPath = conf.Config.PhotoPath.PhotoHost + conf.Config.System.HttpPort + conf.Config.PhotoPath.ProductPath + favorites[i].ImgPath
		}
	}

	resp = &types.DataListResp{
		Item:  favorites,
		Total: total,
	}

	return
}

// FavoriteCreate 创建收藏夹
func (s *FavoriteSrv) FavoriteCreate(ctx context.Context, req *FavoriteCreateReq) (resp interface{}, err error) {
	u, err := ctl.GetUserInfo(ctx)
	if err != nil {
		util.LogrusObj.Error(err)
		return nil, err
	}
	fDao := NewFavoritesDao(ctx)
	exist, _ := fDao.FavoriteExistOrNot(u.Id, req.ProductId)
	if exist {
		err = errors.New("已经存在了")
		util.LogrusObj.Error(err)
		return
	}

	userDao := user.NewUserDao(ctx)
	curUser, err := userDao.GetUserById(u.Id)
	util.LogrusObj.Infof("user: %+v\n", curUser)
	if err != nil {
		util.LogrusObj.Error(err)
		return
	}

	bossDao := user.NewUserDaoByDB(userDao.DB)
	boss, err := bossDao.GetUserById(req.BossId)
	if err != nil {
		util.LogrusObj.Error(err)
		return
	}

	product, err := product.NewProductDao(ctx).GetProductById(req.ProductId)
	if err != nil {
		util.LogrusObj.Error(err)
		return
	}

	favorite := &Favorite{
		UserID:    u.Id,
		User:      *curUser,
		ProductID: req.ProductId,
		Product:   *product,
		BossID:    req.BossId,
		Boss:      *boss,
	}
	err = fDao.CreateFavorite(favorite)
	if err != nil {
		util.LogrusObj.Error(err)
		return
	}

	return
}

// FavoriteDelete 删除收藏夹
func (s *FavoriteSrv) FavoriteDelete(ctx context.Context, req *FavoriteDeleteReq) (resp interface{}, err error) {
	favoriteDao := NewFavoritesDao(ctx)
	var exist bool
	exist, err = favoriteDao.FavoriteExistOrNot(req.Id, req.ProductId)
	if err != nil {
		util.LogrusObj.Error(err)
		return
	}
	if !exist {
		return nil, errors.New("不存在对应收藏夹")
	}
	err = favoriteDao.DeleteFavoriteByUserIdAndProductId(req.Id, req.ProductId)
	if err != nil {
		util.LogrusObj.Error(err)
		return
	}

	return
}
