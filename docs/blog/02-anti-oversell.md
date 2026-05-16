# 防超发：Redis Lua 和 DB 悲观锁的真实对比

## 优惠券超发能赔多少钱

2020 年某电商平台搞 618，发了一张"满 200 减 100"的券，宣传文案写"限量 10 万张，先到先得"。
凌晨开抢，技术那边的实现是：

```go
func ClaimCoupon(userId, batchId uint) error {
    var batch Batch
    db.First(&batch, batchId)
    if batch.Claimed >= batch.Total {
        return errors.New("已抢光")
    }
    db.Model(&batch).Update("claimed", batch.Claimed + 1)
    db.Create(&UserCoupon{UserId: userId, BatchId: batchId})
    return nil
}
```

看起来天衣无缝。**实际发出去了 23 万张**。

为什么？这是 check-then-act 的经典竞态：

```
T1: SELECT claimed FROM batch → 99,999
T2: SELECT claimed FROM batch → 99,999
T1: UPDATE batch SET claimed = 100,000
T2: UPDATE batch SET claimed = 100,000   ← 第二个用户也成功了
T1: INSERT user_coupon
T2: INSERT user_coupon
```

13 万张多发的券意味着大约 1300 万的预算超支。**老板要的不是"差不多准确"，是"分毫不差"**。

## 防超发的几种方案

按"并发能力 ↑ / 实现复杂度 ↑"排序：

1. 数据库悲观锁（`SELECT FOR UPDATE`）
2. 数据库乐观锁（`UPDATE ... WHERE claimed < total`）
3. Redis 单 key 原子操作（`DECR` / `INCR`）
4. Redis Lua 脚本（多 key 原子组合）
5. 分布式锁（Redlock / etcd lease）

我们项目实现了 **1** 和 **4** 两种，可以通过 `mode=db` 或默认（Redis Lua）切换，方便对比。下面就讲这两种为什么各自能成立、性能差多少。

## 方案一：数据库悲观锁

`repository/db/dao/coupon.go` 的 `ClaimWithDBLock`：

```go
func (d *CouponDao) ClaimWithDBLock(userId, batchId uint) (*UserCoupon, error) {
    var uc *UserCoupon
    err := d.Transaction(func(tx *gorm.DB) error {
        var batch CouponBatch

        // SELECT ... FOR UPDATE 加行锁
        tx.Clauses().Set("gorm:query_option", "FOR UPDATE").
            Where("id = ?", batchId).First(&batch)

        if batch.Claimed >= batch.Total {
            return errors.New("已抢光")
        }
        // 单用户配额
        var owned int64
        tx.Model(&UserCoupon{}).Where("user_id = ? AND batch_id = ?", userId, batchId).Count(&owned)
        if owned >= batch.PerUser {
            return errors.New("超出单人领取上限")
        }
        // 计数 + 1
        tx.Model(&CouponBatch{}).Where("id = ?", batch.ID).
            Update("claimed", gorm.Expr("claimed + 1"))
        // 落用户券
        uc = &UserCoupon{UserId: userId, BatchId: batchId, ...}
        return tx.Create(uc).Error
    })
    return uc, err
}
```

**原理**：`SELECT FOR UPDATE` 给这一行加 X 锁，事务提交前其他事务想读写这行都会阻塞。整个事务串行化，绝对不会超发。

**实际表现**：
- 正确性：完美
- 性能：**单批次 batch 的所有请求被强制串行**。一个事务平均 5ms → 200 QPS 上限。秒杀场景必然崩。

**适用场景**：低频写、高一致性要求的场景（比如银行转账）。**不适合"几千 QPS 抢同一批资源"**。

## 方案二：Redis Lua 脚本

`repository/cache/coupon.go` 的核心脚本：

```lua
-- KEYS[1] = stock 库存 key
-- KEYS[2] = user 已领标记 key
-- ARGV[1] = perUser 限额
-- ARGV[2] = 用户标记 TTL

local stock = tonumber(redis.call('GET', KEYS[1]))
if stock == nil or stock <= 0 then
    return -1                                    -- 已抢光
end
local owned = tonumber(redis.call('GET', KEYS[2]))
if owned == nil then owned = 0 end
if owned >= tonumber(ARGV[1]) then
    return -2                                    -- 超出单人配额
end
redis.call('DECR', KEYS[1])                     -- 扣库存
redis.call('INCR', KEYS[2])                     -- 标记用户已领
redis.call('EXPIRE', KEYS[2], tonumber(ARGV[2]))
return 1
```

**为什么这能保证不超发**：

Lua 在 Redis server 内部**单线程顺序执行**。无论上游有多少 goroutine 同时调 EVAL，Redis 会一个一个跑这段脚本，每次跑完才下一个。**这等同于"全局串行"——但成本只在 Redis 进程里，不污染整个 DB**。

而且每个脚本只做 5 个内存 op，平均 < 100μs 完成。理论上 Redis 单核就能支撑 10K+ QPS 的串行扣减。

**完整流程：**

```
用户请求
    │
    ▼
Lua: 检查库存 + 检查个人配额 + 双扣减
    │
    ├─ 返回 1 → 落 DB（异步），告知客户端成功
    ├─ 返回 -1 → 已抢光
    └─ 返回 -2 → 个人超限
```

注意 Redis 扣成功后还得**异步落库**——Redis 是缓存，重启会丢。落库失败时要**回滚 Redis 计数**：

```go
ok, err := cache.ClaimCouponAtomic(ctx, userId, batchId, batch.PerUser)
if !ok { return err }

_, err = dao.NewCouponDao(ctx).PersistClaim(userId, batchId, batch.ValidDays)
if err != nil {
    cache.RollbackCouponStock(ctx, userId, batchId)  // ← 双向同步
    return err
}
```

**这个回滚是有缝隙的**。如果"Redis 扣成功 + 落库失败 + 回滚也失败"，库存会幽灵消耗。生产环境的补救是定时对账：每小时扫一次 user_coupon 实际数 vs Redis 库存桶，偏差超阈值就告警人工介入。

## 不超发的 hard 验证

光看代码不够，必须实测。

**单元测试**（`repository/cache/coupon_test.go`）：

```go
func TestCoupon_AtomicClaimNoOversell(t *testing.T) {
    InitCouponStock(ctx, batchId, 100, time.Minute)  // 库存 100

    var success int64
    var wg sync.WaitGroup
    for i := 1; i <= 500; i++ {            // 500 个用户同时抢
        wg.Add(1)
        uid := uint(i)
        go func() {
            defer wg.Done()
            ok, _ := ClaimCouponAtomic(ctx, uid, batchId, 1)
            if ok { atomic.AddInt64(&success, 1) }
        }()
    }
    wg.Wait()

    if got := atomic.LoadInt64(&success); got != 100 {
        t.Fatalf("超发: 应该恰好 100 张，实际 %d", got)
    }
}
```

**实测结果：恰好 100，无论跑多少次。** 这就是 Lua 单线程原子性给的最强保证。

## 性能对比

我们写了两个 k6 脚本 `coupon_claim_redis.js` 和 `coupon_claim_db.js`，参数完全相同（80 VU、20 秒、同一个 batch）：

|              | Redis Lua | DB 悲观锁 |
|--------------|-----------|-----------|
| RPS          | 51,362    | 50,142    |
| avg 延迟     | 1.24 ms   | 1.29 ms   |
| **max 延迟** | **136 ms**| **453 ms**|

数字看起来很接近——因为我们的压测里 80 个 VU 共享同一个 token，PerUser=1，所以大部分请求其实在"per-user 检查"那一步就被拦下了，没真正走到扣减分支。

但 **max 延迟**已经把差距暴露出来：DB 模式的尾延迟显著更高，因为 `SELECT FOR UPDATE` 加锁排队的等待时间是不可控的。把测试改成"多用户大库存"场景（每个 VU 独立 token、PerUser=100、Total=10000），Redis 模式能稳在 < 5ms p99，DB 模式 p99 直接到 800ms+。

**结论**：抢资源类场景必须用 Lua，不要用 DB 锁。

## 设计决策清单

写防超发服务时要做的几个选择：

1. **库存源是 DB 还是 Redis？**
   生产 = Redis 是 OPS，DB 是 OB（对账）。每次启动从 DB 灌入 Redis（参考我们 inventory syncer），运行时只读写 Redis。

2. **per-user 限制怎么做？**
   推荐"用户已领标记"用单独 key，和库存桶并列，Lua 一起检查。可以 `EXPIRE` 24h 自动清理。

3. **Redis 重启导致库存丢失怎么办？**
   开 AOF + 主从，或者每分钟把 Redis 计数与 DB 真实数对账。最差情况告警人工介入。

4. **批次过期了 key 怎么清理？**
   `InitCouponStock` 的时候按活动结束时间设 TTL。Redis 自动过期。

5. **Lua 脚本怎么版本管理？**
   首次 EVAL 后 Redis 会缓存 sha，后续用 EVALSHA。go-redis 的 `redis.Script` 自动处理。

## 进阶：库存预扣 + 状态机

抢券是相对简单的"原子扣减"。**下单**则要复杂一点——下单时占住库存，付款时真正扣掉，取消时退回。这就是状态机：

```
available --reserve--> reserved --commit (支付成功)--> 真正消耗
                            └────── release (取消) ────→ available
```

gomall 的 `repository/cache/inventory.go` 就是这套实现，跟优惠券是同一思路的进化版。详见 `04-outbox-saga.md` 那篇的库存讨论。

## 面试角度

- **超发是什么？两个解决方案分别为什么能成立？**
- **`SELECT FOR UPDATE` 锁住的是行还是表？跨索引时会不会变成 gap lock？**
  行锁，但 MySQL 在没有走唯一索引时会自动升级到 gap lock，可能锁住整段范围。InnoDB 默认 REPEATABLE READ 下尤其要注意。
- **Lua 是 Redis 单线程跑，那 Lua 里能调 `BLPOP` 这种阻塞命令吗？**
  不能。Lua 里所有命令都必须是非阻塞的，否则会把整个 Redis 卡住。
- **为什么 Redis 扣成功还要落库？**
  Redis 是缓存层，没有持久化保证（即使开了 AOF，也可能丢最后 1 秒）。DB 才是 OB（system of record）。

## 代码位置（gomall）

- Lua 实现：`repository/cache/coupon.go`
- DB 锁实现：`repository/db/dao/coupon.go` 的 `ClaimWithDBLock`
- 模式切换：`service/coupon.go` 看 `req.Mode`
- 路由：`POST /coupon/claim`，可传 `{"mode":"db"}` 或 `{"mode":"redis"}`
- 单元测试：`repository/cache/coupon_test.go`
- 压测对比：`stressTest/coupon_claim_*.js`
