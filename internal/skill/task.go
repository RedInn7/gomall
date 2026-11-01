package skill

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/RedInn7/gomall/internal/order"
	"github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/repository/cache"
)

// orphanTicketGrace 在途 ticket 没有 ticket 状态（抢到名额后、enqueue 写 pending 前崩溃）多久算泄漏。
// enqueue 在抢名额后立刻写 pending，正常远小于这个值；ticket 过期（> OrderTicketTTL）同样没有状态，
// 那时 consumer 已按 order.AsyncTaskMaxAge 不会再处理它。
var orphanTicketGrace = time.Minute

const skillReconcileLockKey = "skill:reconcile:lock"

// snapshotScript 原子读一场活动的 Redis 全貌：{ver, stock, users HGETALL, inflight ZRANGE WITHSCORES, returned SMEMBERS}；
// 库存 key 不存在返回空表（未预热 / 已过期，交给懒预热）。
var snapshotScript = redis.NewScript(`
local stock = redis.call('GET', KEYS[1])
if stock == false then
    return {}
end
return {
    redis.call('GET', KEYS[5]) or '0',
    stock,
    redis.call('HGETALL', KEYS[2]),
    redis.call('ZRANGE', KEYS[3], 0, -1, 'WITHSCORES'),
    redis.call('SMEMBERS', KEYS[4]),
}
`)

// fixScript 版本没变才纠偏（快照到现在没有任何抢 / 还 / 转正发生）。ARGV[1]=快照版本 ARGV[2]=库存增量，
// 随后成对的 userID, 应有件数（<=0 删字段）。
var fixScript = redis.NewScript(luaTouch + `
if (redis.call('GET', KEYS[5]) or '0') ~= ARGV[1] or redis.call('EXISTS', KEYS[1]) == 0 then
    return 0
end
redis.call('INCRBY', KEYS[1], ARGV[2])
for i = 3, #ARGV, 2 do
    if tonumber(ARGV[i + 1]) <= 0 then
        redis.call('HDEL', KEYS[2], ARGV[i])
    else
        redis.call('HSET', KEYS[2], ARGV[i], ARGV[i + 1])
    end
end
touch()
return 1
`)

type inflightTicket struct {
	user   uint
	ticket string
	at     time.Time
}

type skillSnapshot struct {
	ver      string
	stock    int64
	users    map[uint]int64
	inflight []inflightTicket
	returned map[uint]bool
}

// RunSeckillReconcile 逐场核对 Redis 名额与 DB。多实例靠 Redis 锁只跑一个。
//
//  1. 在途 ticket：DB 已有这张单 → 转正（只移出集合）；ticket 已失败，或没有状态超过 orphanTicketGrace → 归还。
//     还在排队（pending）的不动，MQ 积压多久都不会被误补。
//  2. 已关单的订单不在 returned 里 → 归还（关单提交后写 Redis 失败 / 崩溃）。
//  3. 整体对一遍不变式，纠正 Redis 丢数据、人工改动等无法按 ticket 定位的偏差：Redis 快照在一段 Lua 里原子读出，
//     随后在一个 DB 事务里读快照，再确认 Redis 版本没变——期间没有任何抢 / 还 / 转正，两边就是同一时刻的值，
//     差多少纠多少，正负方向都精确；版本变了就重来，最多三次，交给下一轮。
func RunSeckillReconcile() {
	ctx := context.Background()
	ok, err := cache.RedisClient.SetNX(ctx, skillReconcileLockKey, 1, 5*time.Minute).Result()
	if err != nil || !ok {
		return
	}
	defer cache.RedisClient.Del(ctx, skillReconcileLockKey)

	list, err := NewSkillGoodsDao(ctx).ListForReconcile(time.Now())
	if err != nil {
		log.LogrusObj.Errorf("SeckillReconcile 拉取活动失败: %v", err)
		return
	}
	for _, sp := range list {
		reconcileSkill(ctx, sp.Id)
	}
}

func reconcileSkill(ctx context.Context, id uint) {
	snap, v, ok := snapshotWithDB(ctx, id)
	if !ok {
		return
	}
	b := binder{}
	for _, t := range snap.inflight {
		if v.settled[t.ticket] {
			b.Settle(ctx, id, t.user, t.ticket)
			continue
		}
		st, exists, err := order.AsyncOrderTicketStoreInstance().Get(ctx, t.ticket)
		if err != nil {
			log.LogrusObj.Errorf("SeckillReconcile 读 ticket 失败 skill=%d ticket=%s err=%v", id, t.ticket, err)
			continue
		}
		if (exists && st.Status == order.OrderTicketStatusFailed) || (!exists && time.Since(t.at) > orphanTicketGrace) {
			log.LogrusObj.Warnf("SeckillReconcile 归还泄漏名额 skill=%d user=%d ticket=%s", id, t.user, t.ticket)
			release(ctx, id, t.user, t.ticket, true, true)
		}
	}
	for i := range v.closed {
		if o := &v.closed[i]; !snap.returned[o.ID] {
			log.LogrusObj.Warnf("SeckillReconcile 补归还关单名额 skill=%d order=%d", id, o.ID)
			o.SeckillID = id
			b.Return(ctx, o)
		}
	}

	for attempt := 0; attempt < 3; attempt++ {
		if snap, v, ok = snapshotWithDB(ctx, id); !ok {
			return
		}
		ver, err := cache.RedisClient.Get(ctx, cache.SkillKeys(id)[4]).Result()
		if err != nil && err != redis.Nil {
			return
		}
		if ver == "" {
			ver = "0"
		}
		if ver != snap.ver {
			continue // 读 DB 期间 Redis 有变动，两边不是同一时刻
		}
		fixDrift(ctx, id, snap, v)
		return
	}
}

// fixDrift 不变式：stock = num - sold - 在途 - 关单未归还；users[u] = bought[u] + u 的在途 + u 的关单未归还。
func fixDrift(ctx context.Context, id uint, snap *skillSnapshot, v *dbView) {
	want := make(map[uint]int64, len(v.bought))
	for uid, n := range v.bought {
		want[uid] += n
	}
	held := int64(0)
	for _, t := range snap.inflight {
		if !v.settled[t.ticket] { // 已建单的那张已经算在 sold / bought 里
			want[t.user]++
			held++
		}
	}
	for _, o := range v.closed {
		if !snap.returned[o.ID] {
			want[o.UserID]++
			held++
		}
	}
	stockDelta := int64(v.sp.Num-v.sp.Sold) - held - snap.stock
	args := []interface{}{snap.ver, stockDelta}
	for uid, n := range want {
		if snap.users[uid] != n {
			args = append(args, uid, n)
		}
	}
	for uid := range snap.users {
		if _, ok := want[uid]; !ok {
			args = append(args, uid, 0)
		}
	}
	if stockDelta == 0 && len(args) == 2 {
		return
	}
	applied, err := fixScript.Run(ctx, cache.RedisClient, cache.SkillKeys(id), args...).Int()
	if err != nil {
		log.LogrusObj.Errorf("SeckillReconcile 纠偏失败 skill=%d err=%v", id, err)
		return
	}
	if applied == 1 {
		log.LogrusObj.Warnf("SeckillReconcile 纠偏 skill=%d stockDelta=%d users=%v", id, stockDelta, args[2:])
	}
}

// snapshotWithDB 先原子读 Redis，再在一个 DB 事务里读同一场活动。库存 key 不存在返回 false。
func snapshotWithDB(ctx context.Context, id uint) (*skillSnapshot, *dbView, bool) {
	raw, err := snapshotScript.Run(ctx, cache.RedisClient, cache.SkillKeys(id)).Slice()
	if err != nil {
		log.LogrusObj.Errorf("SeckillReconcile 读 Redis 失败 skill=%d err=%v", id, err)
		return nil, nil, false
	}
	if len(raw) == 0 {
		return nil, nil, false
	}
	snap := &skillSnapshot{users: map[uint]int64{}, returned: map[uint]bool{}}
	snap.ver, _ = raw[0].(string)
	snap.stock, _ = strconv.ParseInt(raw[1].(string), 10, 64)
	users, _ := raw[2].([]interface{})
	for i := 0; i+1 < len(users); i += 2 {
		uid, _ := strconv.ParseUint(users[i].(string), 10, 64)
		n, _ := strconv.ParseInt(users[i+1].(string), 10, 64)
		snap.users[uint(uid)] = n
	}
	inflight, _ := raw[3].([]interface{})
	tickets := make([]string, 0, len(inflight)/2)
	for i := 0; i+1 < len(inflight); i += 2 {
		uidStr, ticket, _ := strings.Cut(inflight[i].(string), ":")
		uid, _ := strconv.ParseUint(uidStr, 10, 64)
		ms, _ := strconv.ParseFloat(inflight[i+1].(string), 64)
		snap.inflight = append(snap.inflight, inflightTicket{user: uint(uid), ticket: ticket, at: time.UnixMilli(int64(ms))})
		tickets = append(tickets, ticket)
	}
	returned, _ := raw[4].([]interface{})
	for _, r := range returned {
		oid, _ := strconv.ParseUint(r.(string), 10, 64)
		snap.returned[uint(oid)] = true
	}
	v, err := loadDBView(ctx, id, tickets)
	if err != nil {
		log.LogrusObj.Errorf("SeckillReconcile 读 DB 失败 skill=%d err=%v", id, err)
		return nil, nil, false
	}
	return snap, v, true
}
