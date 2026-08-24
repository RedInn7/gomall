package product

import (
	"context"
	"errors"
	"mime/multipart"
	"strconv"
	"sync"
	"time"

	conf "github.com/RedInn7/gomall/config"
	"github.com/RedInn7/gomall/consts"
	"github.com/RedInn7/gomall/internal/shared/outbox"
	"github.com/RedInn7/gomall/internal/user"
	"github.com/RedInn7/gomall/pkg/utils/ctl"
	"github.com/RedInn7/gomall/pkg/utils/log"
	util "github.com/RedInn7/gomall/pkg/utils/upload"
	"github.com/RedInn7/gomall/repository/cache"
	"github.com/RedInn7/gomall/service/events"
	"github.com/RedInn7/gomall/types"
	"gorm.io/gorm"
)

var ProductSrvIns *ProductSrv
var ProductSrvOnce sync.Once

// ErrProductNotFoundOrForbidden 统一隐藏“商品不存在”和“卖家越权”的差异，避免泄露他人商品是否存在。
var ErrProductNotFoundOrForbidden = errors.New("商品不存在或无权操作")

type ProductSrv struct {
}

func GetProductSrv() *ProductSrv {
	ProductSrvOnce.Do(func() {
		ProductSrvIns = &ProductSrv{}
	})
	return ProductSrvIns
}

// ProductShow Cache Aside 读取商品详情。
//  1. 先读缓存，命中直接返回
//  2. 未命中: SETNX 抢回源锁，单飞回源 DB
//  3. 未抢到锁的请求短暂重试一次，仍未命中则直接回源（兜底）
func (s *ProductSrv) ProductShow(ctx context.Context, req *ProductShowReq) (*ProductResp, error) {
	cached := &ProductResp{}
	if cacheErr := cache.GetProductDetail(ctx, req.ID, cached); cacheErr == nil {
		return cached, nil
	} else if cacheErr == cache.ErrProductNotFound {
		return nil, gorm.ErrRecordNotFound
	} else if cacheErr != cache.ErrProductCacheMiss {
		log.LogrusObj.Warnln("read product cache failed:", cacheErr)
	}

	locked, _ := cache.TryProductLock(ctx, req.ID)
	if !locked {
		time.Sleep(50 * time.Millisecond)
		if cacheErr := cache.GetProductDetail(ctx, req.ID, cached); cacheErr == nil {
			return cached, nil
		} else if cacheErr == cache.ErrProductNotFound {
			return nil, gorm.ErrRecordNotFound
		}
	} else {
		defer cache.UnlockProduct(ctx, req.ID)
	}

	// 进程内 singleflight 合并同 id 的并发回源，叠加在 Redis 互斥锁之上防惊群。
	loaded, err := cache.LoadProductOnce(req.ID, func() (interface{}, error) {
		return s.loadProductFromDB(ctx, req.ID)
	})
	if err != nil {
		// 商品不存在: 写短 TTL 空值标记，挡住后续穿透。
		if errors.Is(err, gorm.ErrRecordNotFound) {
			if setErr := cache.SetProductNotFound(ctx, req.ID); setErr != nil {
				log.LogrusObj.Warnln("write product null cache failed:", setErr)
			}
		}
		return nil, err
	}
	pResp := loaded.(*ProductResp)
	if setErr := cache.SetProductDetail(ctx, req.ID, pResp); setErr != nil {
		log.LogrusObj.Warnln("write product cache failed:", setErr)
	}
	return pResp, nil
}

func (s *ProductSrv) loadProductFromDB(ctx context.Context, id uint) (*ProductResp, error) {
	p, err := NewProductDao(ctx).ShowProductById(id)
	if err != nil {
		log.LogrusObj.Error(err)
		return nil, err
	}
	pResp := &ProductResp{
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

// ProductCreate 创建商品
func (s *ProductSrv) ProductCreate(ctx context.Context, files []*multipart.FileHeader, req *ProductCreateReq) (*Product, error) {
	var err error
	u, err := ctl.GetUserInfo(ctx)
	if err != nil {
		log.LogrusObj.Error("获取用户信息失败，err==", err)
		return nil, err
	}
	uId := u.Id
	boss, err := user.NewUserDao(ctx).GetUserById(uId)
	if err != nil {
		log.LogrusObj.Error("获取卖家信息失败，err:", err)
		return nil, err
	}
	if len(files) == 0 {
		err = errors.New("至少上传一张商品图片")
		log.LogrusObj.Error(err)
		return nil, err
	}
	// 文件上传属于数据库外部 I/O，不能放进 SQL 事务长期占锁。
	// 先上传并收集路径，随后把商品、图片记录和 Outbox 事件一次性提交。
	imagePaths := make([]string, 0, len(files))
	for index, file := range files {
		tmp, openErr := file.Open()
		if openErr != nil {
			log.LogrusObj.Error("打开商品图片失败，err:", openErr)
			return nil, openErr
		}
		fileName := req.Name + strconv.Itoa(index)
		var imagePath string
		if conf.Config.System.UploadModel == consts.UploadModelLocal {
			imagePath, err = util.ProductUploadToLocalStatic(tmp, uId, fileName)
		} else {
			imagePath, err = util.UploadToQiNiu(tmp, file.Size)
		}
		_ = tmp.Close()
		if err != nil {
			log.LogrusObj.Error("上传商品图片失败，err:", err)
			return nil, err
		}
		imagePaths = append(imagePaths, imagePath)
	}
	product := &Product{
		Name:          req.Name,
		CategoryID:    req.CategoryID,
		Title:         req.Title,
		Info:          req.Info,
		ImgPath:       imagePaths[0],
		Price:         req.Price,
		DiscountPrice: req.DiscountPrice,
		Num:           req.Num,
		OnSale:        true,
		BossID:        uId,
		BossName:      boss.UserName,
		BossAvatar:    boss.Avatar,
	}
	if err = createProductAndEvent(NewProductDao(ctx).DB, product, imagePaths...); err != nil {
		log.LogrusObj.Error("创建产品失败，err:", err)
		return nil, err
	}
	// 清掉可能由提前探测未来 ID 写入的空值缓存，让新商品提交后立即可读。
	_ = cache.DelProductDetail(ctx, product.ID)

	return product, nil
}

func (s *ProductSrv) ProductList(ctx context.Context, req *ProductListReq) (*types.DataListResp, error) {
	var total int64
	var err error
	condition := make(map[string]interface{})
	if req.CategoryID != 0 {
		condition["category_id"] = req.CategoryID
	}
	productDao := NewProductDao(ctx)
	products, err := productDao.ListProductByCondition(condition, req.BasePage)
	if err != nil {
		log.LogrusObj.Error(err)
		return nil, err
	}
	// 总数走 60s 缓存 + singleflight：COUNT(*) 是全索引扫描且结果与页码无关，
	// 不缓存时每翻一页都重数一遍全表——列表 p95 2.5s 的根因就在这。
	total, err = cache.ProductCountCached(ctx, req.CategoryID, func() (int64, error) {
		return productDao.CountProductByCondition(condition)
	})
	if err != nil {
		log.LogrusObj.Error(err)
		return nil, err
	}
	pRespList := make([]*ProductResp, 0)
	for _, p := range products {
		pResp := &ProductResp{
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

	return &types.DataListResp{
		Item:  pRespList,
		Total: total,
	}, nil
}

// ProductDelete 删除商品 + 删缓存
func (s *ProductSrv) ProductDelete(ctx context.Context, req *ProductDeleteReq) (*Product, error) {
	u, err := ctl.GetUserInfo(ctx)
	if err != nil {
		log.LogrusObj.Error(err)
		return nil, err
	}
	err = deleteProductAndEvent(NewProductDao(ctx).DB, req.ID, u.Id)
	if err != nil {
		log.LogrusObj.Error(err)
		return nil, err
	}
	_ = cache.DelProductDetail(ctx, req.ID)
	cache.DoubleDeleteAsync(req.ID, 0)
	return nil, nil
}

// insertProductChanged 使用调用方的 tx 写 Outbox。
// 事件与商品事实共用事务，任何一步失败都不会留下“商品已变、事件丢失”的半状态。
func insertProductChanged(tx *gorm.DB, productID uint, op string) error {
	return outbox.NewOutboxDaoByDB(tx).Insert(
		"product", "ProductChanged", "product.changed", productID,
		events.ProductChanged{ProductID: productID, Op: op},
	)
}

// createProductAndEvent 原子写入商品、图库记录和创建事件。
func createProductAndEvent(db *gorm.DB, product *Product, imagePaths ...string) error {
	return db.Transaction(func(tx *gorm.DB) error {
		if err := NewProductDaoByDB(tx).CreateProduct(product); err != nil {
			return err
		}
		imageDao := NewProductImgDaoByDB(tx)
		for _, imagePath := range imagePaths {
			if err := imageDao.CreateProductImg(&ProductImg{ProductID: product.ID, ImgPath: imagePath}); err != nil {
				return err
			}
		}
		return insertProductChanged(tx, product.ID, "create")
	})
}

// updateProductAndEvent 只有归属校验通过时才更新商品并写更新事件。
func updateProductAndEvent(db *gorm.DB, productID, bossID uint, product *Product) error {
	return db.Transaction(func(tx *gorm.DB) error {
		affected, err := NewProductDaoByDB(tx).UpdateProduct(productID, bossID, product)
		if err != nil {
			return err
		}
		if affected == 0 {
			return ErrProductNotFoundOrForbidden
		}
		return insertProductChanged(tx, productID, "update")
	})
}

// deleteProductAndEvent 只有真实删除成功时才写删除事件，避免越权请求产生幽灵事件。
func deleteProductAndEvent(db *gorm.DB, productID, bossID uint) error {
	return db.Transaction(func(tx *gorm.DB) error {
		affected, err := NewProductDaoByDB(tx).DeleteProduct(productID, bossID)
		if err != nil {
			return err
		}
		if affected == 0 {
			return ErrProductNotFoundOrForbidden
		}
		return insertProductChanged(tx, productID, "delete")
	})
}

// ProductUpdate 更新商品，延迟双删保证缓存一致性
//  1. 先删缓存
//  2. 写库（WHERE id=? AND boss_id=? 防止越权覆盖他人商品）
//  3. 异步等 500ms 再删一次（覆盖并发读取旧值后写回的窗口）
func (s *ProductSrv) ProductUpdate(ctx context.Context, req *ProductUpdateReq) (*Product, error) {
	u, err := ctl.GetUserInfo(ctx)
	if err != nil {
		log.LogrusObj.Error(err)
		return nil, err
	}
	product := &Product{
		Name:          req.Name,
		CategoryID:    req.CategoryID,
		Title:         req.Title,
		Info:          req.Info,
		Price:         req.Price,
		DiscountPrice: req.DiscountPrice,
		Num:           req.Num,
		OnSale:        req.OnSale,
	}
	_ = cache.DelProductDetail(ctx, req.ID)
	if err := updateProductAndEvent(NewProductDao(ctx).DB, req.ID, u.Id, product); err != nil {
		log.LogrusObj.Error(err)
		return nil, err
	}
	cache.DoubleDeleteAsync(req.ID, 0)

	return nil, nil
}

// ProductImgList 获取商品图片列表
func (s *ProductSrv) ProductImgList(ctx context.Context, req *ListProductImgReq) (*types.DataListResp, error) {
	productImgs, err := NewProductImgDao(ctx).ListProductImgByProductId(req.ID)
	if err != nil {
		log.LogrusObj.Error(err)
		return nil, err
	}
	for i := range productImgs {
		if conf.Config.System.UploadModel == consts.UploadModelLocal {
			productImgs[i].ImgPath = conf.Config.PhotoPath.PhotoHost + conf.Config.System.HttpPort + conf.Config.PhotoPath.ProductPath + productImgs[i].ImgPath
		}
	}

	return &types.DataListResp{
		Item:  productImgs,
		Total: int64(len(productImgs)),
	}, nil
}
