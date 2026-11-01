package skill

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"golang.org/x/sync/singleflight"
	"gorm.io/gorm"

	"github.com/RedInn7/gomall/internal/address"
	"github.com/RedInn7/gomall/internal/order"
	"github.com/RedInn7/gomall/internal/product"
	"github.com/RedInn7/gomall/pkg/e"
	"github.com/RedInn7/gomall/pkg/utils/ctl"
	"github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/repository/cache"
)

// 秒杀下单时序：
//  1. 时间窗校验（活动详情走缓存）
//  2. 先生成 ticket，grabScript 一段 Lua 原子完成「已购是否到限购 / 剩余 > 0 / 扣秒杀库存 / 记用户名额 / ticket 记进在途集合」
//  3. 走通用异步下单（order.SeckillEnqueue）：未支付单数上限、地址归属、商品两桶预占、ticket、投 MQ
//  4. consumer 以活动价建 WaitPay 单，同事务条件扣活动 sold、记 SkillUser（DB 兜底），提交后 ticket 移出在途集合
//
// Redis 不变式（静止态）：stock + |在途 ticket| + |已关单未归还| == num - sold，
// users[u] == SkillUser.bought[u] + u 的在途 ticket 数 + u 已关单未归还的单数。
// 每个名额的去向都按 ticket / 订单 id 记账，归还按 ticket / 订单幂等，RunSeckillReconcile 逐个核对而不是猜差值。

// skillKeyGrace 活动结束后 Redis 名额 key 再留的时间：结束前下的未支付单在这期间关单还能归还。
const skillKeyGrace = 24 * time.Hour

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

func init() {
	order.RegisterSeckill(binder{})
}

// 回源合并：缓存失效瞬间的并发请求只放一个去查 DB。
var loadGroup singleflight.Group

// 所有改动脚本共用：版本 +1，并让同场活动的其它 key 跟库存 key 同时过期（HASH / ZSET 首次写入时没有 TTL）。
// KEYS 固定为 cache.SkillKeys：1=stock 2=users 3=inflight 4=returned 5=ver
const luaTouch = `
local function touch()
    redis.call('INCR', KEYS[5])
    local ttl = redis.call('TTL', KEYS[1])
    if ttl > 0 then
        for i = 2, 5 do redis.call('EXPIRE', KEYS[i], ttl) end
    end
end
local function decrUser(uid)
    if redis.call('HINCRBY', KEYS[2], uid, -1) <= 0 then
        redis.call('HDEL', KEYS[2], uid)
    end
end
`

// grabScript 抢名额。ARGV[1]=userID ARGV[2]=每人限购 ARGV[3]=在途 member ARGV[4]=当前毫秒
// 返回 1=成功 / -1=抢完 / -2=到限购 / -3=未预热
var grabScript = redis.NewScript(luaTouch + `
local stock = redis.call('GET', KEYS[1])
if stock == false then
    return -3
end
if tonumber(redis.call('HGET', KEYS[2], ARGV[1]) or '0') >= tonumber(ARGV[2]) then
    return -2
end
if tonumber(stock) <= 0 then
    return -1
end
redis.call('DECR', KEYS[1])
redis.call('HINCRBY', KEYS[2], ARGV[1], 1)
redis.call('ZADD', KEYS[3], ARGV[4], ARGV[3])
touch()
return 1
`)

// releaseScript 按 ticket 归还：ticket 还在在途集合里才还，同一 ticket 只还一次。
// ARGV[1]=userID ARGV[2]=在途 member ARGV[3]=是否还库存 ARGV[4]=是否还用户名额；返回 1=已归还 / 0=无可归还
var releaseScript = redis.NewScript(luaTouch + `
if redis.call('ZREM', KEYS[3], ARGV[2]) == 0 then
    return 0
end
if ARGV[4] == '1' then
    decrUser(ARGV[1])
end
if ARGV[3] == '1' and redis.call('EXISTS', KEYS[1]) == 1 then
    redis.call('INCR', KEYS[1])
end
touch()
return 1
`)

// settleScript 建单已提交：ticket 移出在途集合，名额此后由 DB sold / bought 承载。ARGV[1]=在途 member
var settleScript = redis.NewScript(luaTouch + `
if redis.call('ZREM', KEYS[3], ARGV[1]) == 1 then
    touch()
end
return 1
`)

// returnScript 关单已提交：归还库存与用户名额，订单 id 记进 returned，同一订单只还一次。
// 库存 key 不在（过期 / 丢数据）时不记：重新预热按 DB 计算，DB 已包含这次关单。
// ARGV[1]=userID ARGV[2]=订单 id；返回 1=已归还 / 0=无需归还
var returnScript = redis.NewScript(luaTouch + `
if redis.call('EXISTS', KEYS[1]) == 0 then
    return 0
end
if redis.call('SADD', KEYS[4], ARGV[2]) == 0 then
    return 0
end
redis.call('INCR', KEYS[1])
decrUser(ARGV[1])
touch()
return 1
`)

// preheatScript 库存 key 不存在时按 DB 快照重建整场活动，已存在不动（预热重复调用 / 并发懒加载都安全）。
// ARGV[1]=剩余 ARGV[2]=ttl 秒 ARGV[3]=用户数 n，随后 n 对 userID, 件数，再往后是 DB 里已关单的订单 id。
// 在途集合清空：丢掉的在途单落库后 Redis 会多出名额，由 DB 兜底拒单（不加回库存）和对账收敛。
var preheatScript = redis.NewScript(luaTouch + `
if redis.call('EXISTS', KEYS[1]) == 1 then
    return 0
end
redis.call('DEL', KEYS[2], KEYS[3], KEYS[4])
local n = tonumber(ARGV[3])
for i = 0, n - 1 do
    redis.call('HSET', KEYS[2], ARGV[4 + 2 * i], ARGV[5 + 2 * i])
end
for i = 4 + 2 * n, #ARGV do
    redis.call('SADD', KEYS[4], ARGV[i])
end
redis.call('SET', KEYS[1], ARGV[1], 'EX', ARGV[2])
touch()
return 1
`)

// inflightMember 在途集合的 member：userID:ticket
func inflightMember(userId uint, ticket string) string {
	return strconv.FormatUint(uint64(userId), 10) + ":" + ticket
}

// InitSkillGoods 创建秒杀活动并预热 Redis。按 (product_id, start_at) 幂等：同配置重复调用返回已有活动，
// 配置不同报冲突。卖家、原价取自商品表，商品须在售。
func (s *SkillProductSrv) InitSkillGoods(ctx context.Context, req *SkillProductInitReq) (*SkillProduct, error) {
	if !req.EndAt.After(req.StartAt) {
		return nil, e.New(e.InvalidParams)
	}
	limit := req.PerUserLimit
	if limit == 0 {
		limit = 1
	}
	p, err := product.NewProductDao(ctx).GetProductById(req.ProductId)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, e.New(e.ErrorNotExistProduct)
	}
	if err != nil {
		log.LogrusObj.Error(err)
		return nil, err
	}
	if !p.OnSale {
		return nil, errors.New("商品未上架，不能创建秒杀活动")
	}
	origin, ok := p.UnitCents()
	if !ok {
		return nil, fmt.Errorf("商品定价非法 product=%d", p.ID)
	}
	if req.PriceCents > origin {
		return nil, errors.New("秒杀价不能高于商品原价")
	}
	if req.Num > p.Num {
		return nil, errors.New("秒杀库存不能超过商品库存")
	}

	sp := &SkillProduct{
		ProductId:    p.ID,
		BossId:       p.BossID,
		Title:        p.Title,
		PriceCents:   req.PriceCents,
		OriginCents:  origin,
		Num:          req.Num,
		PerUserLimit: limit,
		StartAt:      req.StartAt.Truncate(time.Second),
		EndAt:        req.EndAt.Truncate(time.Second),
	}
	d := NewSkillGoodsDao(ctx)
	existing, err := d.GetByProductStart(sp.ProductId, sp.StartAt)
	if err != nil {
		log.LogrusObj.Error(err)
		return nil, err
	}
	if existing == nil {
		if err = d.Create(sp); err != nil {
			// 并发创建撞唯一索引：按已存在处理
			if existing, _ = d.GetByProductStart(sp.ProductId, sp.StartAt); existing == nil {
				log.LogrusObj.Error(err)
				return nil, err
			}
		}
	}
	if existing != nil {
		if existing.PriceCents != sp.PriceCents || existing.Num != sp.Num ||
			existing.PerUserLimit != sp.PerUserLimit || !existing.EndAt.Equal(sp.EndAt) {
			return nil, e.New(e.ErrSkillConflict)
		}
		sp = existing
	}

	if err = preheat(ctx, sp); err != nil {
		log.LogrusObj.Error(err)
		return nil, err
	}
	// 详情直接写入（覆盖创建前被查过留下的 "null"；回源写空值用 SETNX，不会反过来盖掉它），列表删掉重建。
	body, _ := json.Marshal(sp)
	if err = cache.RedisClient.Set(ctx, fmt.Sprintf(cache.SkillProductKey, sp.Id), body, cache.SkillProductTTL).Err(); err != nil {
		log.LogrusObj.Error(err)
	}
	if err = cache.RedisClient.Del(ctx, cache.SkillProductListKey).Err(); err != nil {
		log.LogrusObj.Error(err)
	}
	return sp, nil
}

// preheat 按 DB 快照（剩余、用户成单数、已关单订单）重建 Redis，key 已存在时不动。结束超过 skillKeyGrace 的活动不建 key。
func preheat(ctx context.Context, sp *SkillProduct) error {
	ttl := time.Until(sp.EndAt.Add(skillKeyGrace))
	if ttl <= 0 {
		return nil
	}
	v, err := loadDBView(ctx, sp.Id, nil)
	if err != nil {
		return err
	}
	args := make([]interface{}, 0, 3+2*len(v.bought)+len(v.closed))
	args = append(args, v.sp.Num-v.sp.Sold, int64(ttl/time.Second)+1, len(v.bought))
	for uid, n := range v.bought {
		args = append(args, uid, n)
	}
	for _, o := range v.closed {
		args = append(args, o.ID)
	}
	return preheatScript.Run(ctx, cache.RedisClient, cache.SkillKeys(sp.Id), args...).Err()
}

// loadSkillProduct 读活动：缓存 → singleflight 回源 DB → 回填。不存在的 id 缓存 "null"，防穿透。
func (s *SkillProductSrv) loadSkillProduct(ctx context.Context, id uint) (*SkillProduct, error) {
	if id == 0 {
		return nil, e.New(e.ErrSkillNotExist)
	}
	key := fmt.Sprintf(cache.SkillProductKey, id)
	raw, err := cache.RedisClient.Get(ctx, key).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		log.LogrusObj.Error(err) // Redis 抖动降级回源
	}
	if err != nil {
		v, err, _ := loadGroup.Do(key, func() (interface{}, error) {
			ctx := context.WithoutCancel(ctx) // 合并后的回源不跟某一个请求的取消走
			sp, err := NewSkillGoodsDao(ctx).GetById(id)
			if err != nil {
				return nil, err
			}
			body, _ := json.Marshal(sp) // sp 为 nil 时即 "null"
			var errx error
			if sp == nil {
				// SETNX：回源查到不存在后活动才创建时，创建写入的详情不会被这里的空值盖掉
				errx = cache.RedisClient.SetNX(ctx, key, body, cache.SkillNullTTL).Err()
			} else {
				errx = cache.RedisClient.Set(ctx, key, body, cache.SkillProductTTL).Err()
			}
			if errx != nil {
				log.LogrusObj.Error(errx)
			}
			return string(body), nil
		})
		if err != nil {
			log.LogrusObj.Error(err)
			return nil, err
		}
		raw = v.(string)
	}

	var sp *SkillProduct
	if err = json.Unmarshal([]byte(raw), &sp); err != nil {
		return nil, err
	}
	if sp == nil {
		return nil, e.New(e.ErrSkillNotExist)
	}
	return sp, nil
}

// ListSkillGoods 未结束的秒杀活动（进行中 + 未开始）。整表一个 JSON 缓存，SkillListTTL 过期。
func (s *SkillProductSrv) ListSkillGoods(ctx context.Context) ([]*SkillProduct, error) {
	raw, err := cache.RedisClient.Get(ctx, cache.SkillProductListKey).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		log.LogrusObj.Error(err)
	}
	if err != nil {
		v, err, _ := loadGroup.Do(cache.SkillProductListKey, func() (interface{}, error) {
			ctx := context.WithoutCancel(ctx)
			list, err := NewSkillGoodsDao(ctx).ListNotEnded(time.Now())
			if err != nil {
				return nil, err
			}
			if list == nil {
				list = []*SkillProduct{}
			}
			body, _ := json.Marshal(list)
			if errx := cache.RedisClient.Set(ctx, cache.SkillProductListKey, body, cache.SkillListTTL).Err(); errx != nil {
				log.LogrusObj.Error(errx)
			}
			return string(body), nil
		})
		if err != nil {
			log.LogrusObj.Error(err)
			return nil, err
		}
		raw = v.(string)
	}

	var list []*SkillProduct
	if err = json.Unmarshal([]byte(raw), &list); err != nil {
		return nil, err
	}
	// 缓存期间结束的活动不再展示
	now := time.Now()
	resp := make([]*SkillProduct, 0, len(list))
	for _, sp := range list {
		if sp.EndAt.After(now) {
			resp = append(resp, sp)
		}
	}
	return resp, nil
}

// resolveSkill 按活动 id 取活动；只给了商品 id（旧调用方式）时取该商品进行中的场次，没有就取最近一场未开始的。
func (s *SkillProductSrv) resolveSkill(ctx context.Context, skillId, productId uint) (*SkillProduct, error) {
	if skillId != 0 || productId == 0 {
		return s.loadSkillProduct(ctx, skillId)
	}
	list, err := s.ListSkillGoods(ctx) // 按开始时间升序，第一场匹配的就是进行中或最近的
	if err != nil {
		return nil, err
	}
	for _, sp := range list {
		if sp.ProductId == productId {
			return sp, nil
		}
	}
	return nil, e.New(e.ErrSkillNotExist)
}

// GetSkillGoods 秒杀活动详情 + 剩余库存
func (s *SkillProductSrv) GetSkillGoods(ctx context.Context, req *GetSkillProductReq) (*SkillProductDetail, error) {
	sp, err := s.resolveSkill(ctx, req.SkillProductId, req.ProductId)
	if err != nil {
		return nil, err
	}
	resp := &SkillProductDetail{SkillProduct: sp}
	if !sp.EndAt.After(time.Now()) {
		return resp, nil // 已结束，剩余按 0 展示
	}
	n, err := cache.RedisClient.Get(ctx, cache.SkillStockKey(sp.Id)).Int64()
	if errors.Is(err, redis.Nil) {
		if err = preheat(ctx, sp); err == nil {
			n, err = cache.RedisClient.Get(ctx, cache.SkillStockKey(sp.Id)).Int64()
		}
	}
	if err != nil {
		log.LogrusObj.Error(err)
		return nil, err
	}
	resp.Remaining = max(n, 0)
	return resp, nil
}

// SkillProduct 秒杀下单：返回异步下单 ticket。
func (s *SkillProductSrv) SkillProduct(ctx context.Context, req *SkillProductReq) (*order.OrderEnqueueResp, error) {
	u, err := ctl.GetUserInfo(ctx)
	if err != nil {
		log.LogrusObj.Error(err)
		return nil, err
	}
	sp, err := s.resolveSkill(ctx, req.SkillProductId, req.ProductId)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	if now.Before(sp.StartAt) {
		return nil, e.New(e.ErrSkillNotStarted)
	}
	if !now.Before(sp.EndAt) {
		return nil, e.New(e.ErrSkillEnded)
	}
	addressId := req.AddressId
	if addressId == 0 { // 没传收货地址：用用户的第一个地址
		list, err := address.NewAddressDao(ctx).GetAddressByuId(u.Id)
		if err != nil {
			log.LogrusObj.Error(err)
			return nil, err
		}
		if len(list) == 0 {
			return nil, errors.New("请先添加收货地址")
		}
		addressId = list[0].ID
	}

	ticket := order.NewTicket()
	if err = grab(ctx, sp, u.Id, ticket); err != nil {
		return nil, err
	}
	resp, err := order.GetOrderSrv().SeckillEnqueue(ctx, &order.OrderCreateReq{
		ProductID: sp.ProductId,
		Num:       1,
		AddressID: addressId,
	}, sp.Id, ticket)
	if err != nil {
		log.LogrusObj.Errorf("seckill enqueue failed skill=%d user=%d err=%v", sp.Id, u.Id, err)
		release(context.WithoutCancel(ctx), sp.Id, u.Id, ticket, true, true)
		return nil, err
	}
	return &resp, nil
}

// grab 跑抢名额 Lua；key 不存在（Redis 丢数据 / 预热失败）时按 DB 懒预热后重试一次。
func grab(ctx context.Context, sp *SkillProduct, userId uint, ticket string) error {
	keys := cache.SkillKeys(sp.Id)
	for i := 0; i < 2; i++ {
		code, err := grabScript.Run(ctx, cache.RedisClient, keys,
			userId, sp.PerUserLimit, inflightMember(userId, ticket), time.Now().UnixMilli()).Int64()
		if err != nil {
			log.LogrusObj.Error(err)
			return err
		}
		switch code {
		case 1:
			return nil
		case -1:
			return e.New(e.ErrSkillSoldOut)
		case -2:
			return e.New(e.ErrSkillLimitExceeded)
		}
		if err = preheat(ctx, sp); err != nil {
			log.LogrusObj.Error(err)
			return err
		}
	}
	return errors.New("秒杀库存预热失败")
}

// release 按 ticket 归还 Redis 名额，幂等。失败只记日志：ticket 留在在途集合里，RunSeckillReconcile 会处理。
func release(ctx context.Context, skillId, userId uint, ticket string, stock, user bool) {
	err := releaseScript.Run(ctx, cache.RedisClient, cache.SkillKeys(skillId),
		userId, inflightMember(userId, ticket), boolArg(stock), boolArg(user)).Err()
	if err != nil {
		log.LogrusObj.Errorf("seckill release quota failed skill=%d user=%d ticket=%s err=%v", skillId, userId, ticket, err)
	}
}

func boolArg(b bool) int {
	if b {
		return 1
	}
	return 0
}

// binder 实现 order.SeckillBinder。秒杀单每单 1 件。
type binder struct{}

func (binder) Price(ctx context.Context, seckillID, productID uint) (int64, uint, error) {
	sp, err := NewSkillGoodsDao(ctx).GetById(seckillID)
	if err != nil {
		return 0, 0, err
	}
	if sp == nil {
		return 0, 0, e.New(e.ErrSkillNotExist)
	}
	if sp.ProductId != productID || sp.PriceCents <= 0 {
		return 0, 0, fmt.Errorf("秒杀活动与商品不匹配或定价非法 skill=%d product=%d", seckillID, productID)
	}
	// 和普通下单一样要求商品还在、在售、卖家没变：否则下架商品还能成交，外部渠道收了钱却履约不了。
	p, err := product.NewProductDao(ctx).GetProductById(productID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, 0, e.New(e.ErrorNotExistProduct)
	}
	if err != nil {
		return 0, 0, err
	}
	if !p.OnSale || p.BossID != sp.BossId {
		return 0, 0, fmt.Errorf("秒杀商品已下架或卖家变更 skill=%d product=%d", seckillID, productID)
	}
	return sp.PriceCents, sp.BossId, nil
}

func (binder) BindTx(tx *gorm.DB, o *order.Order) error {
	d := NewSkillGoodsDaoByDB(tx)
	sp, err := d.GetById(o.SeckillID)
	if err != nil {
		return err
	}
	if sp == nil {
		return e.New(e.ErrSkillNotExist)
	}
	return d.Occupy(sp, o.UserID, o.Num)
}

func (binder) UnbindTx(tx *gorm.DB, o *order.Order) error {
	return NewSkillGoodsDaoByDB(tx).Release(o.SeckillID, o.UserID, o.Num)
}

func (binder) Settle(ctx context.Context, seckillID, userID uint, ticket string) {
	if err := settleScript.Run(ctx, cache.RedisClient, cache.SkillKeys(seckillID), inflightMember(userID, ticket)).Err(); err != nil {
		log.LogrusObj.Errorf("seckill settle failed skill=%d ticket=%s err=%v", seckillID, ticket, err)
	}
}

// Abort DB 兜底拒单说明 Redis 那一侧多了：售罄只还用户名额（库存多出的这一件就此消掉），
// 超限购只还库存（用户名额少记的这一件就此补上）；其它失败两样都还。
func (binder) Abort(ctx context.Context, seckillID, userID uint, ticket string, cause error) {
	switch e.CodeOf(cause) {
	case e.ErrSkillSoldOut:
		release(ctx, seckillID, userID, ticket, false, true)
	case e.ErrSkillLimitExceeded:
		release(ctx, seckillID, userID, ticket, true, false)
	default:
		release(ctx, seckillID, userID, ticket, true, true)
	}
}

func (binder) Return(ctx context.Context, o *order.Order) {
	if err := returnScript.Run(ctx, cache.RedisClient, cache.SkillKeys(o.SeckillID), o.UserID, o.ID).Err(); err != nil {
		log.LogrusObj.Errorf("seckill return quota failed skill=%d order=%d err=%v", o.SeckillID, o.ID, err)
	}
}
