package product

import (
	"context"
	"errors"
	"mime/multipart"
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

	lockToken, locked, _ := cache.TryProductLock(ctx, req.ID)
	if !locked {
		time.Sleep(50 * time.Millisecond)
		if cacheErr := cache.GetProductDetail(ctx, req.ID, cached); cacheErr == nil {
			return cached, nil
		} else if cacheErr == cache.ErrProductNotFound {
			return nil, gorm.ErrRecordNotFound
		}
	} else {
		defer cache.UnlockProduct(ctx, req.ID, lockToken)
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
	product := &Product{
		Name:               req.Name,
		CategoryID:         req.CategoryID,
		Title:              req.Title,
		Info:               req.Info,
		Price:              req.Price,
		DiscountPrice:      req.DiscountPrice,
		Num:                req.Num,
		OnSale:             true,
		BossID:             uId,
		BossName:           boss.UserName,
		BossAvatar:         boss.Avatar,
		GroupbuyPriceCents: req.GroupbuyPriceCents,
	}
	// 拼团价在上传图片之前校验，非法请求不落盘
	if err = product.checkGroupbuyPrice(); err != nil {
		log.LogrusObj.Error(err)
		return nil, err
	}
	// 文件上传属于数据库外部 I/O，不能放进 SQL 事务长期占锁。
	// 先上传并收集路径，随后把商品、图片记录和 Outbox 事件一次性提交。
	imagePaths := make([]string, 0, len(files))
	for _, file := range files {
		tmp, openErr := file.Open()
		if openErr != nil {
			log.LogrusObj.Error("打开商品图片失败，err:", openErr)
			return nil, openErr
		}
		var imagePath string
		if conf.Config.System.UploadModel == consts.UploadModelLocal {
			imagePath, err = util.ProductUploadToLocalStatic(tmp)
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
	product.ImgPath = imagePaths[0]
	if err = createProductAndEvent(NewProductDao(ctx).DB, product, imagePaths...); err != nil {
		log.LogrusObj.Error("创建产品失败，err:", err)
		return nil, err
	}
	// 清掉可能由提前探测未来 ID 写入的空值缓存，让新商品提交后立即可读。
	_ = cache.DelProductDetail(ctx, product.ID)
	// 建库存桶，新商品提交后立即可下单。商品已落库，写 Redis 不跟着请求取消；
	// 失败只记日志，下单时 ReserveStock 会按 DB 库存补建。
	if err = cache.InitStock(context.WithoutCancel(ctx), product.ID, int64(product.Num)); err != nil {
		log.LogrusObj.Errorf("init stock product=%d failed: %v", product.ID, err)
	}

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

// updateProductAndEvent 锁住归属于 bossID 的商品行，只写请求里传了的字段，并写更新事件。
// 返回改之前的库存 oldNum：行锁保证它和这次写入之间没有并发的付款扣减，调用方据此算库存差值。
func updateProductAndEvent(db *gorm.DB, productID, bossID uint, req *ProductUpdateReq) (oldNum int, err error) {
	err = db.Transaction(func(tx *gorm.DB) error {
		productDao := NewProductDaoByDB(tx)
		p, err := productDao.GetProductForUpdate(productID, bossID)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrProductNotFoundOrForbidden
		}
		if err != nil {
			return err
		}
		oldNum = p.Num
		updates := req.applyTo(p)
		// 改了零售价、折后价或拼团价时，按合并后的新值校验拼团价，改价不会留下越界的拼团价
		if req.Price != nil || req.DiscountPrice != nil || req.GroupbuyPriceCents != nil {
			if err = p.checkGroupbuyPrice(); err != nil {
				return err
			}
		}
		if len(updates) == 0 {
			return nil
		}
		if err = productDao.UpdateProduct(productID, bossID, updates); err != nil {
			return err
		}
		return insertProductChanged(tx, productID, "update")
	})
	return oldNum, err
}

// deleteProductAndEvent 只有真实删除成功时才写删除事件，避免越权请求产生幽灵事件。
func deleteProductAndEvent(db *gorm.DB, productID, bossID uint) error {
	return db.Transaction(func(tx *gorm.DB) error {
		// favorite.product_id 有外键指向 product，商品又是物理删除：先删收藏，否则 MySQL 报 1451。
		// favorite 包依赖 product，这里不能反过来 import，直接按表名删。不是本人的商品时
		// 下面删商品影响 0 行，整个事务回滚，收藏不会被误删。
		if err := tx.Exec("DELETE FROM favorite WHERE product_id = ?", productID).Error; err != nil {
			return err
		}
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
	if err = req.checkPrices(); err != nil {
		log.LogrusObj.Error(err)
		return nil, err
	}
	_ = cache.DelProductDetail(ctx, req.ID)
	oldNum, err := updateProductAndEvent(NewProductDao(ctx).DB, req.ID, u.Id, req)
	if err != nil {
		log.LogrusObj.Error(err)
		return nil, err
	}
	cache.DoubleDeleteAsync(req.ID, 0)
	// 改了库存：Redis available 按差值同步（算法见 cache.AdjustAvailable）。DB 已提交，写 Redis
	// 不跟着请求取消；失败只记日志，留下的偏差由定时任务 ReconcileAvailable 纠正。
	if req.Num != nil && *req.Num != oldNum {
		delta := int64(*req.Num - oldNum)
		if err = cache.AdjustAvailable(context.WithoutCancel(ctx), req.ID, delta); err != nil {
			log.LogrusObj.Errorf("adjust stock product=%d delta=%d failed: %v", req.ID, delta, err)
		}
	}

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
