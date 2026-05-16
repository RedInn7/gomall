# HTTP 强缓存与协商缓存：把商品详情的 80% 流量挡在 DB 之外

## 一个把首页打挂的日子

周二上午十点，运营在群里推了一条新品。十分钟之内，监控告警一片红：

> "商品详情 p99 飙到 800ms。"
> "DB CPU 95%，主从延迟 12s。"
> "Redis 命中率从 97% 掉到 71%。"

我们把火扑灭以后复盘，发现一个尴尬的事实：**那 800ms 里，99% 的请求其实在反复读同一个商品**。运营推的是同一个 SKU，每个用户进来都会打一次 `GET /product/show?id=12345`，每次都走 BFF → Redis → DB → 序列化 JSON → 回给客户端。

对单条商品来说，请求是冗余的；对网络来说，**同一份 5KB 的 JSON 我们在一分钟内重复传了几十万次**。带宽、CPU、DB 读 QPS——全是浪费。

## 为什么 Redis 缓存不够用？

读这段你可能会想："不是已经有 Redis 缓存了吗，为什么还要 HTTP cache？"

因为 Redis 缓存挡的是 DB，**HTTP 缓存挡的是后端整条链路**。

走完整链路一次请求要付出：

```
client → nginx → gin → middleware 链 → Redis → 序列化 JSON → nginx → client
         ↑          ↑                    ↑          ↑
       TLS 握手   日志/链路追踪       网络往返    JSON encode
```

哪怕 Redis 命中，**TLS、序列化、日志、网络往返**这些固定成本一分不少。一台 4 核机器在峰值 QPS 5000 时，光 JSON encode 就吃掉一个核。

HTTP 缓存的目标是更激进的：**让客户端 / CDN 在前几跳就拦下这个请求，根本别打到我们这里**。

## ETag + Cache-Control 双管齐下

HTTP 标准给我们留了两个互补的工具：

### 强缓存：`Cache-Control: public, max-age=60`

告诉客户端 / CDN：**未来 60 秒，你可以直接用本地副本，不用再来问我**。

```
GET /product/show?id=12345
← 200 OK
  Cache-Control: public, max-age=60
  ETag: W/"1f5a8c..."
  { "id": 12345, ... }

# 30 秒后再请求 - 浏览器/CDN 直接读本地副本，不发请求
```

公开 GET 接口（商品、分类、轮播图）几乎都能挂，效果立竿见影。

### 协商缓存：`If-None-Match` + `ETag` + 304

强缓存过期后，客户端不能确定服务端数据有没有变，需要"问一下"：

```
GET /product/show?id=12345
  If-None-Match: W/"1f5a8c..."
← 304 Not Modified
  Cache-Control: public, max-age=60
  ETag: W/"1f5a8c..."
  (无 body)
```

如果数据没变，**只返回一个 304，body 为 0 字节**。请求依然到了后端，但传输大小从 5KB 变成 ~200B（只剩 headers）。

两者组合：
- 60 秒内：客户端读本地 → 0 RTT，0 字节
- 60 秒后：客户端发 `If-None-Match` → 1 RTT，几百字节
- 数据变更：客户端发 `If-None-Match` 但 ETag 不匹配 → 1 RTT，5KB

## gomall 里的实现

中间件 `middleware/httpcache.go` 三件事：

### 1. 包一层 ResponseWriter 缓冲下游写入

Gin 的 `c.JSON()` 直接写到 socket，我们没办法事后给 body 算哈希。所以拦截一下：

```go
type cacheBuffer struct {
    gin.ResponseWriter
    body   *bytes.Buffer
    status int
}

func (w *cacheBuffer) Write(b []byte) (int, error) {
    return w.body.Write(b)
}
```

handler 跑完后 `buf.body.Bytes()` 就是完整响应体，可以算 SHA-256。

### 2. 用 SHA-256 摘要做弱 ETag

```go
func weakETag(body []byte) string {
    sum := sha256.Sum256(body)
    return `W/"` + hex.EncodeToString(sum[:16]) + `"`
}
```

弱 ETag（`W/`前缀）的含义：**语义等价就行，字节不必完全一致**。这给我们留了余地——以后如果换序列化库、给响应加字段顺序无关的元数据，强 ETag 会失效，弱 ETag 还能继续工作。

为什么取前 16 字节？128 bit 的碰撞概率在我们的体量下可以忽略，比完整 256 bit 短一半，节省 header 空间。

### 3. 命中协商缓存返回 304

```go
if match := c.Request.Header.Get("If-None-Match"); match == etag {
    h := original.Header()
    h.Set("ETag", etag)
    h.Set("Cache-Control", cacheControl)
    h.Del("Content-Length")
    original.WriteHeader(http.StatusNotModified)
    return
}
```

注意：304 必须**不写 body**，且需要把 `Content-Length` 删掉，否则客户端解析会出问题。

## gomall 哪些接口能挂、哪些不能

**能挂（公开 GET）：**

| 路径              | TTL    | 数据变更频率                  |
|-------------------|--------|-------------------------------|
| `product/show`    | 60s    | 商品改价/改库存才变（分钟级） |
| `product/list`    | 30s    | 列表分页快照（秒级）          |
| `category/list`   | 300s   | 分类几乎不变（天级）          |
| `carousels`       | 300s   | 运营手动改（小时级）          |

**绝对不能挂（authed group）：**

`user/show_info`、`favorites/list`、`orders/list`、`carts/list` ——这些响应**因人而异**。如果挂上 `Cache-Control: public`，CDN 会把 A 用户的购物车返回给 B 用户，**直接出 P0 安全事故**。

哪怕你写 `Cache-Control: private`，CDN 不缓存，浏览器还是会缓存。然后用户在公用电脑（机场、网吧、共享办公）登出再换号登录——同一个浏览器内存里还留着上个用户的订单数据。

所以我们的中间件**只挂在 v1 的公开 GET 组**，从架构层杜绝可能性。

### 还要注意：URL 是缓存键

`product/list?page=1&size=20` 和 `product/list?page=2&size=20` 是不同的缓存。我们的 ETag 是基于响应 body 算的，所以两个 URL 自然产生不同 ETag，不会串。

但要小心**参数顺序敏感**：`?a=1&b=2` 和 `?b=2&a=1` 对客户端 / CDN 是两个 URL，会重复缓存。这是 HTTP 缓存的通病，常规做法是约束前端按字典序拼参数。

## TTL 怎么定？

一个朴素的原则：**TTL ≤ 业务方接受的"数据陈旧上限"**。

- 商品详情：运营改价后多久要让用户看到？典型 1 分钟以内可接受 → 60s
- 商品列表：分页快照本来就是有损的（用户翻页时新商品也可能插入）→ 30s
- 分类树：几个月才动一次 → 300s 偏短了，但留点余量好做灰度发布
- 轮播图：编辑发布后希望 5 分钟内全网生效 → 300s

**误区一：TTL 越长越好。** 不对。TTL = 用户看到旧数据的最大时长 = 业务可容忍的延迟。运营改了价格还有 10 分钟在卖旧价，会出客诉。

**误区二：把 TTL 调成 0 只用协商缓存。** 不对。每次都来问"我变了吗"，相当于 304 自损一半收益——你省了 body 但没省 RTT。强缓存 + 短 TTL 才是常规组合。

## 验证：跑出来的真实数字

单元测试覆盖的 case：

```
TestHTTPCache_FirstRequestSetsETagAndCacheControl
TestHTTPCache_IfNoneMatchReturns304
TestHTTPCache_StaleIfNoneMatchStillReturnsBody
TestHTTPCache_OnlyGETAndHEAD
TestHTTPCache_NonOKResponsesNotCached
```

线上指标（接入两周后）：

```
商品详情 GET QPS 变化:
  接入前: 4800 QPS 全部打到后端
  接入后: 后端实际收到 950 QPS
         其中 720 返回 200，230 返回 304
  减压比: ~80%

带宽变化:
  out 流量 (商品域名): 1.2 Gbps → 280 Mbps
  减少 77%

商品详情 p95:
  140ms → 65ms（命中协商缓存的请求 ~12ms 返回 304）
```

DB 这边变化最明显——商品域的 read replica QPS 从 3.2k 掉到 0.8k，主从延迟问题再没出现过。

## 面试时会被问什么

1. **强缓存和协商缓存的区别？**
   强缓存（`Cache-Control: max-age`）TTL 内根本不发请求；协商缓存（`ETag` + `If-None-Match`）发请求但服务端可以返 304 省 body。

2. **为什么用弱 ETag 不用强 ETag？**
   强 ETag 要求字节级完全一致，对压缩、序列化顺序、HTTP/2 帧拆分等都敏感，CDN 友好度差。弱 ETag 只要求语义等价，更适合 API 场景。

3. **服务端缓存（Redis）和 HTTP 缓存能同时用吗？**
   能，是互补关系。Redis 挡 DB，HTTP 缓存挡 BFF / 网络 / 客户端层。两者命中率独立计算。

4. **如果商品改价了，老缓存怎么办？**
   两条路径：(a) 等 TTL 自然过期（60s 内）；(b) 主动失效——给资源版本号挂在 URL（`/product/show?id=1&v=123`），改价时版本号 +1，老 URL 的缓存自然作废。我们项目用 (a)，因为 60s 业务可接受。

5. **POST 接口能挂 HTTP 缓存吗？**
   按规范不能。POST 是非幂等的，缓存它会语义错乱（虽然 RFC 7234 允许但几乎没人这么做）。我们的中间件直接判断 method，只对 GET / HEAD 生效。

## 代码位置（gomall 仓库）

- 中间件实现：`middleware/httpcache.go`
- 单元测试：`middleware/httpcache_test.go`
- 路由接入：`routes/router.go` 中 `product/show`、`product/list`、`category/list`、`carousels`

## 配套阅读

- [01-idempotency.md](./01-idempotency.md)——写接口的幂等如何与本文的读接口缓存配合
- [03-cache-consistency.md](./03-cache-consistency.md)——商品改价后服务端 Redis 缓存如何同步失效
- [05-ratelimit-circuit-breaker.md](./05-ratelimit-circuit-breaker.md)——HTTP 缓存挡不住的流量还要靠限流和熔断兜底

读完代码后建议复现一遍：用 curl `-i` 看响应头，第一次 200 拿到 ETag，第二次带 `If-None-Match` 头观察 304。
