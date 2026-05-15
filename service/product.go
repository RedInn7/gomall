package service

import (
	"context"
	"errors"
	"mime/multipart"
	"strconv"
	"sync"
	"time"

	conf "github.com/RedInn7/gomall/config"
	"github.com/RedInn7/gomall/consts"
	"github.com/RedInn7/gomall/pkg/utils/ctl"
	"github.com/RedInn7/gomall/pkg/utils/log"
	util "github.com/RedInn7/gomall/pkg/utils/upload"
	"github.com/RedInn7/gomall/repository/cache"
	"github.com/RedInn7/gomall/repository/db/dao"
	"github.com/RedInn7/gomall/repository/db/model"
	"github.com/RedInn7/gomall/types"
)

var ProductSrvIns *ProductSrv
var ProductSrvOnce sync.Once

type ProductSrv struct {
}

func GetProductSrv() *ProductSrv {
	ProductSrvOnce.Do(func() {
		ProductSrvIns = &ProductSrv{}
	})
	return ProductSrvIns
}

// ProductShow Cache Aside 读取商品详情。
//   1. 先读缓存，命中直接返回
//   2. 未命中: SETNX 抢回源锁，单飞回源 DB
//   3. 未抢到锁的请求短暂重试一次，仍未命中则直接回源（兜底）
func (s *ProductSrv) ProductShow(ctx context.Context, req *types.ProductShowReq) (resp interface{}, err error) {
	cached := &types.ProductResp{}
	if cacheErr := cache.GetProductDetail(ctx, req.ID, cached); cacheErr == nil {
		return cached, nil
	} else if cacheErr != cache.ErrProductCacheMiss {
		log.LogrusObj.Warnln("read product cache failed:", cacheErr)
	}

	locked, _ := cache.TryProductLock(ctx, req.ID)
	if !locked {
		time.Sleep(50 * time.Millisecond)
		if cacheErr := cache.GetProductDetail(ctx, req.ID, cached); cacheErr == nil {
			return cached, nil
		}
	} else {
		defer cache.UnlockProduct(ctx, req.ID)
	}

	pResp, err := s.loadProductFromDB(ctx, req.ID)
	if err != nil {
		return nil, err
	}
	if setErr := cache.SetProductDetail(ctx, req.ID, pResp); setErr != nil {
		log.LogrusObj.Warnln("write product cache failed:", setErr)
	}
	return pResp, nil
}

func (s *ProductSrv) loadProductFromDB(ctx context.Context, id uint) (*types.ProductResp, error) {
	p, err := dao.NewProductDao(ctx).ShowProductById(id)
	if err != nil {
		log.LogrusObj.Error(err)
		return nil, err
	}
	pResp := &types.ProductResp{
		ID:            p.ID,
		Name:          p.Name,
		CategoryID:    p.CategoryID,
		Title:         p.Title,
		Info:          p.Info,
		ImgPath:       p.ImgPath,
		Price:         p.Price,
		DiscountPrice: p.DiscountPrice,
		View:          p.View(),
		CreatedAt:     p.CreatedAt.Unix(),
		Num:           p.Num,
		OnSale:        p.OnSale,
		BossID:        p.BossID,
		BossName:      p.BossName,
		BossAvatar:    p.BossAvatar,
	}
	if conf.Config.System.UploadModel == consts.UploadModelLocal {
		pResp.BossAvatar = conf.Config.PhotoPath.PhotoHost + conf.Config.System.HttpPort + conf.Config.PhotoPath.AvatarPath + pResp.BossAvatar
		pResp.ImgPath = conf.Config.PhotoPath.PhotoHost + conf.Config.System.HttpPort + conf.Config.PhotoPath.ProductPath + pResp.ImgPath
	}
	return pResp, nil
}

// 创建商品
func (s *ProductSrv) ProductCreate(ctx context.Context, files []*multipart.FileHeader, req *types.ProductCreateReq) (resp interface{}, err error) {
	u, err := ctl.GetUserInfo(ctx)
	if err != nil {
		log.LogrusObj.Error("获取用户信息失败，err==", err)
		return nil, err
	}
	uId := u.Id
	boss, err := dao.NewUserDao(ctx).GetUserById(uId)
	if err != nil {
		log.LogrusObj.Error("获取卖家信息失败，err:", err)
		return nil, err
	}
	if len(files) == 0 {
		err = errors.New("至少上传一张商品图片")
		log.LogrusObj.Error(err)
		return nil, err
	}
	// 以第一张作为封面图
	tmp, err := files[0].Open()
	if err != nil {
		log.LogrusObj.Error("打开封面图失败，err:", err)
		return nil, err
	}
	var path string
	if conf.Config.System.UploadModel == consts.UploadModelLocal {
		path, err = util.ProductUploadToLocalStatic(tmp, uId, req.Name)
	} else {
		path, err = util.UploadToQiNiu(tmp, files[0].Size)
	}
	tmp.Close()
	if err != nil {
		log.LogrusObj.Error("上传图片失败，err:", err)
		return
	}
	product := &model.Product{
		Name:          req.Name,
		CategoryID:    req.CategoryID,
		Title:         req.Title,
		Info:          req.Info,
		ImgPath:       path,
		Price:         req.Price,
		DiscountPrice: req.DiscountPrice,
		Num:           req.Num,
		OnSale:        true,
		BossID:        uId,
		BossName:      boss.UserName,
		BossAvatar:    boss.Avatar,
	}
	productDao := dao.NewProductDao(ctx)
	err = productDao.CreateProduct(product)
	if err != nil {
		log.LogrusObj.Error("创建产品失败，err:", err)
		return
	}

	for index, file := range files {
		num := strconv.Itoa(index)
		tmp, openErr := file.Open()
		if openErr != nil {
			log.LogrusObj.Error("打开商品图片失败，err:", openErr)
			return nil, openErr
		}
		if conf.Config.System.UploadModel == consts.UploadModelLocal {
			path, err = util.ProductUploadToLocalStatic(tmp, uId, req.Name+num)
		} else {
			path, err = util.UploadToQiNiu(tmp, file.Size)
		}
		tmp.Close()
		if err != nil {
			log.LogrusObj.Error(err)
			return
		}
		productImg := &model.ProductImg{
			ProductID: product.ID,
			ImgPath:   path,
		}
		err = dao.NewProductImgDaoByDB(productDao.DB).CreateProductImg(productImg)
		if err != nil {
			log.LogrusObj.Error(err)
			return
		}
	}

	return
}

func (s *ProductSrv) ProductList(ctx context.Context, req *types.ProductListReq) (resp interface{}, err error) {
	var total int64
	condition := make(map[string]interface{})
	if req.CategoryID != 0 {
		condition["category_id"] = req.CategoryID
	}
	productDao := dao.NewProductDao(ctx)
	products, err := productDao.ListProductByCondition(condition, req.BasePage)
	if err != nil {
		log.LogrusObj.Error(err)
		return
	}
	total, err = productDao.CountProductByCondition(condition)
	if err != nil {
		log.LogrusObj.Error(err)
		return
	}
	pRespList := make([]*types.ProductResp, 0)
	for _, p := range products {
		pResp := &types.ProductResp{
			ID:            p.ID,
			Name:          p.Name,
			CategoryID:    p.CategoryID,
			Title:         p.Title,
			Info:          p.Info,
			ImgPath:       p.ImgPath,
			Price:         p.Price,
			DiscountPrice: p.DiscountPrice,
			View:          p.View(),
			CreatedAt:     p.CreatedAt.Unix(),
			Num:           p.Num,
			OnSale:        p.OnSale,
			BossID:        p.BossID,
			BossName:      p.BossName,
			BossAvatar:    p.BossAvatar,
		}
		if conf.Config.System.UploadModel == consts.UploadModelLocal {
			pResp.BossAvatar = conf.Config.PhotoPath.PhotoHost + conf.Config.System.HttpPort + conf.Config.PhotoPath.AvatarPath + pResp.BossAvatar
			pResp.ImgPath = conf.Config.PhotoPath.PhotoHost + conf.Config.System.HttpPort + conf.Config.PhotoPath.ProductPath + pResp.ImgPath
		}
		pRespList = append(pRespList, pResp)
	}

	resp = &types.DataListResp{
		Item:  pRespList,
		Total: total,
	}

	return
}

// ProductDelete 删除商品 + 删缓存
func (s *ProductSrv) ProductDelete(ctx context.Context, req *types.ProductDeleteReq) (resp interface{}, err error) {
	u, err := ctl.GetUserInfo(ctx)
	if err != nil {
		log.LogrusObj.Error(err)
		return
	}
	err = dao.NewProductDao(ctx).DeleteProduct(req.ID, u.Id)
	if err != nil {
		log.LogrusObj.Error(err)
		return
	}
	_ = cache.DelProductDetail(ctx, req.ID)
	cache.DoubleDeleteAsync(req.ID, 0)
	return
}

// ProductUpdate 更新商品，延迟双删保证缓存一致性
//   1. 先删缓存
//   2. 写库
//   3. 异步等 500ms 再删一次（覆盖并发读取旧值后写回的窗口）
func (s *ProductSrv) ProductUpdate(ctx context.Context, req *types.ProductUpdateReq) (resp interface{}, err error) {
	product := &model.Product{
		Name:       req.Name,
		CategoryID: req.CategoryID,
		Title:      req.Title,
		Info:       req.Info,
		// ImgPath:       service.ImgPath,
		Price:         req.Price,
		DiscountPrice: req.DiscountPrice,
		OnSale:        req.OnSale,
	}
	_ = cache.DelProductDetail(ctx, req.ID)
	err = dao.NewProductDao(ctx).UpdateProduct(req.ID, product)
	if err != nil {
		log.LogrusObj.Error(err)
		return
	}
	cache.DoubleDeleteAsync(req.ID, 0)

	return
}

// 搜索商品 TODO 后续用脚本同步数据MySQL到ES，用ES进行搜索
func (s *ProductSrv) ProductSearch(ctx context.Context, req *types.ProductSearchReq) (resp interface{}, err error) {
	products, count, err := dao.NewProductDao(ctx).SearchProduct(req.Info, req.BasePage)
	if err != nil {
		log.LogrusObj.Error(err)
		return
	}

	pRespList := make([]*types.ProductResp, 0)
	for _, p := range products {
		pResp := &types.ProductResp{
			ID:            p.ID,
			Name:          p.Name,
			CategoryID:    p.CategoryID,
			Title:         p.Title,
			Info:          p.Info,
			ImgPath:       p.ImgPath,
			Price:         p.Price,
			DiscountPrice: p.DiscountPrice,
			View:          p.View(),
			CreatedAt:     p.CreatedAt.Unix(),
			Num:           p.Num,
			OnSale:        p.OnSale,
			BossID:        p.BossID,
			BossName:      p.BossName,
			BossAvatar:    p.BossAvatar,
		}
		if conf.Config.System.UploadModel == consts.UploadModelLocal {
			pResp.BossAvatar = conf.Config.PhotoPath.PhotoHost + conf.Config.System.HttpPort + conf.Config.PhotoPath.AvatarPath + pResp.BossAvatar
			pResp.ImgPath = conf.Config.PhotoPath.PhotoHost + conf.Config.System.HttpPort + conf.Config.PhotoPath.ProductPath + pResp.ImgPath
		}
		pRespList = append(pRespList, pResp)
	}

	resp = &types.DataListResp{
		Item:  pRespList,
		Total: count,
	}

	return
}

// ProductImgList 获取商品列表图片
func (s *ProductSrv) ProductImgList(ctx context.Context, req *types.ListProductImgReq) (resp interface{}, err error) {
	productImgs, err := dao.NewProductImgDao(ctx).ListProductImgByProductId(req.ID)
	if err != nil {
		log.LogrusObj.Error(err)
		return
	}
	for i := range productImgs {
		if conf.Config.System.UploadModel == consts.UploadModelLocal {
			productImgs[i].ImgPath = conf.Config.PhotoPath.PhotoHost + conf.Config.System.HttpPort + conf.Config.PhotoPath.ProductPath + productImgs[i].ImgPath
		}
	}

	resp = &types.DataListResp{
		Item:  productImgs,
		Total: int64(len(productImgs)),
	}

	return
}
