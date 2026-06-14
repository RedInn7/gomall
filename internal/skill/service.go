package skill

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/redis/go-redis/v9"

	"github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/repository/cache"
)

var SkillProductSrvIns *SkillProductSrv
var SkillProductSrvOnce sync.Once

type SkillProductSrv struct {
}

func GetSkillProductSrv() *SkillProductSrv {
	SkillProductSrvOnce.Do(func() {
		SkillProductSrvIns = &SkillProductSrv{}
	})
	return SkillProductSrvIns
}

// InitSkillGoods 初始化秒杀商品并预热缓存。成功不回传数据，data 为 null。
func (s *SkillProductSrv) InitSkillGoods(ctx context.Context) (resp []*SkillProduct, err error) {
	spList := make([]*SkillProduct, 0)
	for i := 1; i < 10; i++ {
		spList = append(spList, &SkillProduct{
			ProductId: uint(i),
			BossId:    2,
			Title:     "秒杀商品",
			Money:     200,
			Num:       10,
		})
	}
	err = NewSkillGoodsDao(ctx).BatchCreate(spList)
	if err != nil {
		log.LogrusObj.Infoln(err)
		return nil, err
	}

	// 落库的同时写入缓存：列表 key 供秒杀列表读取，
	// per-product key 供秒杀详情/下单按 ProductId 直接命中。
	for i := range spList {
		jsonBytes, errx := json.Marshal(spList[i])
		if errx != nil {
			log.LogrusObj.Infoln(errx)
			return nil, errx
		}
		jsonString := string(jsonBytes)
		_, errx = cache.RedisClient.LPush(ctx, cache.SkillProductListKey, jsonString).Result()
		if errx != nil {
			log.LogrusObj.Infoln(errx)
			return nil, errx
		}
		errx = cache.RedisClient.Set(ctx,
			fmt.Sprintf(cache.SkillProductKey, spList[i].ProductId),
			jsonString, cache.SkillProductTTL).Err()
		if errx != nil {
			log.LogrusObj.Infoln(errx)
			return nil, errx
		}
	}

	return nil, nil
}

// loadSkillProduct 读取单个秒杀商品的 JSON，缓存未命中时回源 DB 并回填，
// 避免依赖 InitSkillGoods 预热顺序导致接口必然失败。
func (s *SkillProductSrv) loadSkillProduct(ctx context.Context, productId uint) (string, error) {
	rc := cache.RedisClient
	key := fmt.Sprintf(cache.SkillProductKey, productId)
	resp, err := rc.Get(ctx, key).Result()
	if err == nil {
		return resp, nil
	}
	if !errors.Is(err, redis.Nil) {
		log.LogrusObj.Infoln(err)
		return "", err
	}

	sp, err := NewSkillGoodsDao(ctx).GetByProductId(productId)
	if err != nil {
		log.LogrusObj.Infoln(err)
		return "", err
	}
	jsonBytes, err := json.Marshal(sp)
	if err != nil {
		log.LogrusObj.Infoln(err)
		return "", err
	}
	jsonString := string(jsonBytes)
	if errx := rc.Set(ctx, key, jsonString, cache.SkillProductTTL).Err(); errx != nil {
		log.LogrusObj.Infoln(errx)
	}

	return jsonString, nil
}

// ListSkillGoods 秒杀商品列表。
// 返回值异构：缓存命中时为 []string（原始 JSON 串），缓存未命中回源 DB 时为
// []*SkillProduct（结构化对象）。两条分支类型不同，故保留 interface{}。
func (s *SkillProductSrv) ListSkillGoods(ctx context.Context) (resp interface{}, err error) {
	rc := cache.RedisClient
	skillProductList, err := rc.LRange(ctx, cache.SkillProductListKey, 0, -1).Result()
	if err != nil {
		log.LogrusObj.Infoln(err)
		return nil, err
	}

	if len(skillProductList) == 0 {
		skillGoods, errx := NewSkillGoodsDao(ctx).ListSkillGoods()
		if errx != nil {
			log.LogrusObj.Infoln(errx)
			return nil, errx
		}

		for i := range skillGoods {
			jsonBytes, errx := json.Marshal(skillGoods[i])
			if errx != nil {
				log.LogrusObj.Infoln(errx)
				return nil, errx
			}
			jsonString := string(jsonBytes)
			_, errx = rc.LPush(ctx, cache.SkillProductListKey, jsonString).Result()
			if errx != nil {
				log.LogrusObj.Infoln(errx)
				return nil, errx
			}
		}
		resp = skillGoods
	} else {
		resp = skillProductList
	}

	return
}

// GetSkillGoods 秒杀商品详情
func (s *SkillProductSrv) GetSkillGoods(ctx context.Context, req *GetSkillProductReq) (resp string, err error) {
	return s.loadSkillProduct(ctx, req.ProductId)
}

// SkillProduct 秒杀下单
func (s *SkillProductSrv) SkillProduct(ctx context.Context, req *SkillProductReq) (resp string, err error) {
	return s.loadSkillProduct(ctx, req.ProductId)
}
