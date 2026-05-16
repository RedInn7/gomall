# 缓存一致性：为什么我必须删两次

## 你以为很简单的题

面试时被问"商品详情更新时，缓存怎么处理？"

很多人脱口而出：

> "先更新数据库，再删缓存。"

**就这一句话，能挂掉一半候选人。** 不是因为它错，是因为面试官接下来会问："你画一下这个时序图。" 然后挂。

我们今天来把这道题彻底吃透。

## 缓存一致性问题的本质

只要数据**同时存在两个地方**（DB 和 Redis），就有"两者状态不同步"的窗口。这个窗口短则微秒、长则几秒，取决于：

- 读写分离（DB 主从延迟）
- 网络抖动
- GC 暂停
- 进程崩溃

我们要做的不是消灭窗口（不可能），而是让窗口内被读到的脏数据**对用户无害**或**自动收敛**。

## 候选方案大评比

| 方案                | 一致性     | 性能       | 工程复杂度 |
|---------------------|------------|------------|------------|
| 先更新 DB，再删缓存 | 99% 场景 OK | 高         | 低         |
| 先删缓存，再更新 DB | 容易脏数据 | 高         | 低         |
| 双写（DB + Cache）  | 强但易错   | 高         | 中         |
| 延迟双删            | 接近强一致 | 中         | 中         |
| 订阅 binlog 同步    | 最终强一致 | 高         | **高**     |

我们项目用的是**"先删缓存 → 写 DB → 异步再删一次"**，俗称延迟双删。下面解释为什么。

## 把"先更新 DB 再删缓存"画时序图

假设商品 ID=1，原价 100：

```
时间 →

A 线程（读）：   GET cache(id=1)  miss   ┐
                                          │
B 线程（写）：                   UPDATE db SET price=200
                                          │
B 线程（写）：                   DEL cache(id=1)
                                          │
A 线程（读）：   SELECT db → price=100 (从只读副本读出旧数据) ┐
                                                              │
A 线程（读）：   SET cache(id=1, price=100)   ← 把旧数据塞回去了
```

**结果**：缓存里写回了旧价格。下次所有用户都看到 100，直到下一次更新或 TTL 过期。

注意这个 race 的精妙之处：
1. A 读 DB 之前，B 已经更新过 DB 了
2. 但 **A 读的是只读副本（slave）**，B 的更新可能还没同步过来
3. A 拿到的是旧数据，写回缓存
4. B 已经删过缓存了，但删的是 "B 删完之前" 的状态

主从延迟下这种 race 非常常见。**绝大多数生产事故都来自这种"读副本 + 写主库"的延迟**。

## 错误方案：先删缓存再写 DB

为了"修"上面的问题，有人提出反过来：

```
DEL cache(id=1)  →  UPDATE db
```

同样画时序图：

```
A 线程（读）：   GET cache(id=1)  miss
B 线程（写）：   DEL cache(id=1)
A 线程（读）：   SELECT db → 旧值
B 线程（写）：   UPDATE db
A 线程（读）：   SET cache(id=1, 旧值)
```

**问题没解决，只是窗口换了个位置。**

## 延迟双删的思路

既然单删的窗口逻辑上修不掉，那就**删两次**：

```
1. DEL cache              ← 先把脏数据清掉
2. UPDATE db              ← 写新数据
3. sleep(500ms)           ← 等"读到旧值的并发请求"把数据写回来
4. DEL cache 再来一次     ← 把写回来的脏数据再清掉
```

第二次删的目的是：**清掉那些在第 1 步之后、第 2 步之前读到旧 DB 数据的请求把数据塞回缓存的痕迹**。

**这不是强一致，是"窗口期可控的最终一致"**：
- 第一次删之后 → 第二次删之前 → 这段时间（默认 500ms）内仍可能读到旧值
- 第二次删之后 → 数据最终正确

500ms 这个数值要根据你的"读流量 + 主从延迟"来定。我们项目用 500ms 是个保守的中庸值。

## 我们代码长什么样

`service/product.go` 里 `ProductUpdate`：

```go
func (s *ProductSrv) ProductUpdate(ctx, req) {
    product := &model.Product{...}

    _ = cache.DelProductDetail(ctx, req.ID)         // 1. 先删
    err = dao.NewProductDao(ctx).UpdateProduct(...)  // 2. 写库
    if err != nil { return err }

    cache.DoubleDeleteAsync(req.ID, 0)               // 3. 异步等 500ms 再删一次
    return
}
```

第三步是异步的，不阻塞响应。即使第二次删失败也只是稍微多挂一个 TTL 周期，不致命。

## 读路径：Cache Aside + 防击穿

写路径只是一半。读路径同样要小心**缓存击穿**。

场景：一个热门商品的缓存 key 突然过期，1000 个并发请求同时打过来，全部 cache miss，全部去查 DB。DB 瞬间被 1000 个相同查询打挂。

我们的解法是 **SETNX 抢回源锁**：

```go
func (s *ProductSrv) ProductShow(ctx, req) {
    // 1. 读缓存
    if cache.GetProductDetail(ctx, req.ID, cached) == nil {
        return cached
    }
    // 2. 缓存 miss，尝试拿"回源锁"
    locked := cache.TryProductLock(ctx, req.ID)
    if !locked {
        time.Sleep(50 * time.Millisecond)  // 没拿到锁的请求短暂等待
        if cache.GetProductDetail(ctx, req.ID, cached) == nil {
            return cached    // 等到了，从缓存读
        }
        // 还没等到，兜底直接查 DB (避免无限等待)
    } else {
        defer cache.UnlockProduct(ctx, req.ID)
    }
    // 3. 单飞回源
    pResp := loadFromDB(req.ID)
    cache.SetProductDetail(ctx, req.ID, pResp)
    return pResp
}
```

`TryProductLock` 就是个 Redis SETNX 带 3 秒 TTL。**只有第一个请求会真的查 DB**，其他请求 sleep 一下就能从缓存读到结果。

这是 "single flight" 模式的 Redis 实现，比 Go 的 `singleflight` 包好处是**跨进程生效**（多个 gomall 实例共享同一把锁）。

## 为什么不用"订阅 binlog"？

理论上最优雅的方案：

```
DB binlog → Canal → 消费者 → 写 Redis
```

整个流水线异步，业务代码完全不用管缓存。

**为什么我们没做**：
1. 部署成本高：要装 Canal、配置 binlog、维护一个独立的消费者集群
2. 单点故障：Canal 挂了，缓存就脱缓
3. 教学/演示场景不必要

**生产真的有这需求时**，参考阿里巴巴的 Canal + RocketMQ 方案，或者你自己用 Outbox 模式（gomall 已经上了 Outbox，扩展一下就能搞）。

## 还有哪些坑

### 1. 写 DB 但删缓存失败

如果"写 DB 成功，但 DEL 网络异常"，缓存里就是脏数据，等 TTL 过期才会消失。

**应对**：
- 写一个 redis-failed-deletes 表，定时重试
- 或者在 Outbox 里发一个 `product.cache.invalidate` 事件，由消费者保证最终删除

### 2. 多个缓存层

如果除了 Redis 还有 CDN、本地内存缓存（如 caffeine），你需要"逐层失效"。最难的是 CDN：它有自己的 TTL，业务方一般只能等过期或者主动调 purge API。

**最佳实践**：能不缓存的就不缓存。可缓存的，**写路径尽量少**——下单类操作不缓存，只缓存读多写少的字典数据。

### 3. 缓存雪崩

千万级用户、几万个 key 在同一个时间点 TTL 过期 → 雪崩式回源。

**应对**：TTL 加随机抖动 `ttl = baseTTL + rand(0, jitterTTL)`，避免集中过期。

## 面试时的标准回答

问："商品详情更新时，怎么保证缓存一致性？"

答的层次：
1. **基础**：先更新 DB，再删缓存（说出最常用的方案）
2. **进阶**：但有主从延迟下的 race（画时序图说明），所以用延迟双删
3. **再进阶**：读路径要做防击穿，用 SETNX 抢回源锁
4. **架构层**：如果一致性要求极高，可以接 binlog 同步——但成本高，一般业务不值得

**展开"再进阶"那两点的人，基本就是 P6 以上的水平了**。

## 代码位置（gomall）

- 写路径（延迟双删）：`service/product.go` 的 `ProductUpdate`
- 读路径（Cache Aside + 防击穿）：`service/product.go` 的 `ProductShow`
- 底层 Redis 操作：`repository/cache/product.go`

## 想自己验证？

跑两组压测对比：
1. `stressTest/product_show.js`：当前实现，看 p95 延迟和 RPS
2. 把 `ProductShow` 里的缓存逻辑注释掉，重跑

我们项目 6M 行 order 表 + 1M 行 product 表，缓存命中下 p95 = 3ms，无缓存直接打 DB 大概率是 50ms+。**这就是缓存值得做的工程价值**。
