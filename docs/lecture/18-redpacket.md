# 18 红包：Redis 分发与 DB 记账的一致性

> 抢红包同时是高并发分发和小额资金转移。Redis 决定谁能拿到哪一份，DB 决定钱到底有没有动。这一讲沿 `internal/redpacket` 和 `repository/cache/redpacket.go` 讲清楚：两边可能在哪里对不上，代码又是怎样让它们收敛到同一个结果的。

## 本讲目标

讲完后，学生应该能：

- 说清二倍均值预拆和 Lua `LPOP` 抢包的分工，以及为什么热路径上不算金额；
- 画出发包、领包、退款三段经过平台托管账户的复式分录，解释 `ref` 和 `biz_type` 怎样设计才幂等；
- 解释领取事务和过期退款之间靠哪一把行锁串行，为什么 `FOR UPDATE` 必须放在 `SUM` 前面；
- 说清领取事务失败后什么时候把份额还给 Redis、什么时候不能还；
- 解释为什么发包补偿和过期回收共用一个退款函数，而且退款金额只按 DB 算。

---

## 一、先看三张客诉单

**本节判断：红包的资金以 DB 为准，Redis 只负责发份额；所有事故都出在这两边的交界处。**

上线后客服可能收到三类工单：

| 工单 | 用户看到的现象 | 本讲对应位置 |
|---|---|---|
| A | 点了“抢”，接口报错；再点一次，这份还能不能抢 | 4.1、4.4 |
| B | 最后一份被抢走的同一秒红包过期，发包人退款金额和领取人金额加起来会不会超过总额 | 4.2、4.3 |
| C | 发红包扣了钱，但所有人都抢不了 | 第五节 |

三张单子各对应一种机制：工单 A 靠 Saga 回补把份额还给 Redis，工单 B 靠红包行锁让领取和退款串行，工单 C 靠立即补偿加定时兜底。

先看模块分工。存储结构写在缓存文件顶部（`repository/cache/redpacket.go:15-20`）：

```go
// 红包在 Redis 的存储结构：
//   redpacket:{id}:amounts   LIST  发包时一次性 RPUSH 预拆好的金额数组，LPOP 即抢
//   redpacket:{id}:claimed   HASH  userID -> 抢到金额 (兜底单用户去重)
```

DB 侧两张表（`internal/redpacket/model.go:21-41`）：

```go
type RedPacket struct {
	dbmodel.Model
	UserID    uint      // 发包人
	Total     int64     // 总额，单位：分
	Count     int       // 红包总份数
	Remaining int       // 剩余份数，领取事务内递减
	ExpireAt  time.Time // 过期时间
	Status    uint      // 1 active / 2 expired / 3 finished / 4 refunded
	Greeting  string    `gorm:"size:128"`
}

type RedPacketClaim struct {
	dbmodel.Model
	RedPacketID uint  `gorm:"not null;uniqueIndex:uk_rp_user,priority:1"`
	UserID      uint  `gorm:"not null;uniqueIndex:uk_rp_user,priority:2;index"`
	Amount      int64 `gorm:"not null"` // 抢到金额，单位：分
}
// （省略 gorm 标签中与本讲无关的部分）
```

记住一条守恒式，后面每一节都在守它：

```text
Total = SUM(red_packet_claim.amount) + 退回发包人的金额
```

它只用 DB 里的数算，Redis 里的数一个都不参与。

接入层的护栏只用一句话带过：发包挂 `Idempotency`，抢包挂按用户的 `SlidingWindow`（3 次/秒）和 `Idempotency`（`internal/redpacket/routes.go:17-25`），原理见第 15 讲。

---

## 二、分发模型：发包时拆好，抢包时只 LPOP

**本节判断：金额在发包时一次算完，抢包的热路径只做一次原子出队，不做任何计算和加锁。**

### 2.1 二倍均值预拆

`SplitRedPacket`（`repository/cache/redpacket.go:102-142`）：

```go
func SplitRedPacket(total int64, count int) ([]int64, error) {
	if count <= 0 || total < int64(count) {
		return nil, ErrRedPacketSplitInvalid
	}
	out := make([]int64, count)
	remain := total
	splitRand.Lock()
	defer splitRand.Unlock()

	for i := 0; i < count-1; i++ {
		left := int64(count - i)
		avg := remain / left * 2           // 二倍均值上界
		if avg < 2 {
			avg = 2
		}
		maxThisRound := remain - int64(count-1-i) // 给后面每份留 1 分
		// （省略 maxThisRound < 1 的兜底）
		hi := avg - 1
		if hi > maxThisRound {
			hi = maxThisRound
		}
		// （省略 hi < 1 的兜底）
		amt := splitRand.r.Int63n(hi) + 1  // [1, hi]
		out[i] = amt
		remain -= amt
	}
	out[count-1] = remain                  // 最后一份拿剩下的全部
	splitRand.r.Shuffle(count, func(i, j int) { out[i], out[j] = out[j], out[i] })
	return out, nil
}
```

三条规则要讲清楚：

1. **每份至少 1 分。** 入口先校验 `total >= count`（`internal/redpacket/service.go:53-55`），每轮的上界不超过 `remain - 后面份数`。
2. **总和严格等于 total。** 前 `count-1` 份随机，最后一份直接取余数，不存在舍入误差。
3. **单份期望等于当前均值。** 每轮在 `[1, 2*avg-1]` 上均匀取值，期望约等于 `remain/left`，所以“先抢后抢”的期望大致相同。

最后一份没有上界：前面每轮都抽到小值时，它可能远大于均值。代码用 `Shuffle` 打乱顺序，让这份大额不固定落在最后一个抢的人手里。这只是打乱位置，分布本身没变。

`splitRand` 是带锁的独立随机源（`repository/cache/redpacket.go:92-96`），注释说明是为了避开 `math/rand` 全局源的锁竞争。拆包只在发包时调用，不在抢包热路径上。

入口还有两个敞口限制：单个红包上限 200 元（`internal/redpacket/dto.go:5`、`service.go:56-58`），有效期最长 7 天（`service.go:68-74`）。

### 2.2 一个 Lua 写入，一个 Lua 抢

发包时把整组金额一次 `RPUSH` 进 LIST（`repository/cache/redpacket.go:34-43`），抢包时一个脚本完成去重、出队和标记（`repository/cache/redpacket.go:52-63`）：

```lua
if redis.call('HEXISTS', KEYS[2], ARGV[1]) == 1 then
    return -2                      -- 已领过
end
local amt = redis.call('LPOP', KEYS[1])
if not amt then
    return -1                      -- 已抢完
end
redis.call('HSET', KEYS[2], ARGV[1], amt)
redis.call('EXPIRE', KEYS[2], tonumber(ARGV[2]))
return tonumber(amt)
```

为什么不在抢包时实时算金额？实时拆需要读当前剩余金额和份数、计算、写回，三步必须原子，要么上分布式锁，要么把整个二倍均值算法写进 Lua。预拆以后，抢包只剩一个 `LPOP`，Redis 单线程执行脚本，天然不会超发，也不需要 `WATCH/MULTI`。

代价是金额序列在发包时就定了，写进 Redis 后不可再调整。对红包这种业务，这个代价可以接受。

和第 12 讲库存预占的思路一样：Redis 先扣，DB 为准，失败回补。区别是库存扣的是数量，这里扣的是一份带金额的值，回补时要把同一个金额还回去。

---

## 三、资金模型：钱先进托管，再分给领取人

**本节判断：发包时钱不直接进任何领取人的钱包，而是先进平台托管账户；三段资金流各自是一组复式分录，各自幂等。**

### 3.1 三段分录

`settle.go` 顶部注释写明了三段（`internal/redpacket/settle.go:15-22`）：

| 阶段 | 函数 | ref | biz_type | 分录 |
|---|---|---|---|---|
| 发包 | `settleSendTx` | 红包 id | `redpacket_send` | 发包人 debit / 平台 credit |
| 领包 | `settleClaimTx` | 领取记录 id | `redpacket_claim` | 平台 debit / 领取人 credit |
| 退款 | `settleRefundTx` | 红包 id | `redpacket_refund` | 平台 debit / 发包人 credit |

平台账户是 `user_id = 0`。一个 100 元、3 份的红包，领走 30 + 50，过期退 20：

```text
发包  发包人 debit 10000   平台 credit 10000
领包  平台 debit 3000      领取人1 credit 3000
领包  平台 debit 5000      领取人2 credit 5000
退款  平台 debit 2000      发包人 credit 2000
平台账户净额 = 10000 - 3000 - 5000 - 2000 = 0
```

平台账户在一个红包生命周期结束后归零，这就是对账时检查守恒的依据。

### 3.2 ref 和 biz_type 怎么选

台账唯一键是 `(ref_order_id, direction, biz_type)`（`internal/money/ledger_model.go:64-72`）。

- 发包和退款都用红包 id 做 ref，平台账户一个 credit、一个 debit，方向不同；但发包人也是一 debit 一 credit。如果共用一个 `biz_type`，发包人的退款 credit 会和平台的发包 credit 撞同一个唯一键。所以拆成 `redpacket_send` 和 `redpacket_refund`（`ledger_model.go:31-36`）。关键在于唯一键里没有 `user_id`：同一个 ref 下，“发包人退款 credit”和“平台发包 credit”方向相同，只能靠 `biz_type` 区分。
- 领包用领取记录 id 做 ref。同一个红包有多个领取人，如果用红包 id，第二个领取人的 credit 就会撞第一个人的唯一键。

### 3.3 改余额和写台账在同一事务

`adjustWallet`（`internal/redpacket/settle.go:27-73`）：

```go
func adjustWallet(tx *gorm.DB, userID, refID uint, delta int64, bizType string) (skipped bool, balanceAfter int64, err error) {
	// （省略 direction 推导）
	var existing int64
	if err = tx.Model(&money.AccountTransaction{}).
		Where("ref_order_id=? AND direction=? AND biz_type=?", refID, direction, bizType).
		Count(&existing).Error; err != nil {
		return false, 0, err
	}
	if existing > 0 {
		return true, 0, nil                  // 已入过账，跳过
	}
	userDao := user.NewUserDaoByDB(tx)
	u, err := userDao.GetUserByIdForUpdate(userID)
	// （省略 err 判断）
	bal, err := u.DecryptMoney()
	// （省略 err 判断）
	balanceAfter = bal + delta
	if delta < 0 && balanceAfter < 0 {
		return false, 0, user.ErrInsufficientBalance   // 不允许透支
	}
	// （省略加密余额写回 UpdateUserMoney、amount 取绝对值）
	if err = money.NewLedgerDaoByDB(tx).AppendTransaction(userID, refID, direction, amount, balanceAfter, bizType); err != nil {
		return false, 0, err
	}
	return false, balanceAfter, nil
}
```

先查后写的预检加唯一索引兜底，加密余额加 `FOR UPDATE`，这套做法和第 02 讲余额支付一致，不再展开。平台账户没有 `users.money` 行，`adjustClearing` 只追加流水，`balance_after` 记 0（`settle.go:77-97`）。

发包时整笔都在一个事务里（`internal/redpacket/service.go:92-111`）：写红包主记录、`settleSendTx`、写 outbox。余额不足时整笔回滚。测试 `TestSendRejectsInsufficientBalance`（`internal/redpacket/service_test.go:23-43`）没有调用 `Create`，而是手写了同样形态的事务（建红包 + `settleSendTx`，不含 outbox），验证余额、台账和红包表都没有残留。发包前还要校验支付密码（`service.go:60-66`），和 `paydown` 一样。

### 3.4 一组分录的两条腿：先用户，后平台

三个 `settleXxxTx` 的结构完全一样，以发包为例（`internal/redpacket/settle.go:101-113`）：

```go
func settleSendTx(tx *gorm.DB, rpID, senderID uint, total int64) error {
	if total <= 0 {
		return errors.New("redpacket settle: invalid total")
	}
	skipped, _, err := adjustWallet(tx, senderID, rpID, -total, money.BizTypeRedPacketSend)
	if err != nil {
		return err
	}
	if skipped {
		return nil
	}
	return adjustClearing(tx, rpID, total, money.BizTypeRedPacketSend)
}
```

几个设计点：

- **用户腿在前。** 用户钱包这一侧有余额校验和行锁，最可能失败；它失败时平台腿还没写，事务回滚也没有半边分录要清理。
- **`skipped` 一起短路。** 用户腿已经入过账，说明这一组分录之前整组提交过（两条腿总在同一事务里），平台腿不用再看。`adjustClearing` 自己也带同样的 `(ref, direction, biz_type)` 预检（`settle.go:87-95`），即使被单独调用也不会重复记账。
- **金额边界各有口径。** 发包和领包遇到非正数直接报错（`settle.go:102-104`、`118-120`），因为这两段一定有钱在动；退款的 `left <= 0` 返回 nil（`settle.go:133-135`），因为红包被领光后过期回收时剩余就是 0，这是正常情况，不是错误。

方向由 `delta` 的正负推出，金额取绝对值写台账，调用方只需要关心“谁加、谁减”。同一个 `ref` 下两条腿方向相反，所以平台腿和用户腿不会互相撞唯一键。

---

## 四、一致性边界：Redis 先扣，DB 说了算

**本节判断：Redis 出队成功只代表“拿到了一份的资格”，钱是否到账由领取事务决定；领取和退款必须串行在同一行锁上。**

### 4.1 领取主链路

```mermaid
sequenceDiagram
    participant Client
    participant Service as RedPacketSrv
    participant Redis
    participant DB as MySQL

    Client->>Service: POST /redpacket/claim (id)
    activate Service
    Service->>DB: Get(id) (status, expire_at)
    DB-->>Service: RedPacket
    Service->>Redis: claim Lua (amounts, claimed, uid)
    activate Redis
    Redis-->>Service: amount / -1 / -2
    deactivate Redis
    alt 已抢完或已领过
        Service-->>Client: 失败 (红包已抢完 / 已领取过本红包)
    else 抢到 amount
        Service->>DB: BEGIN，INSERT claim，UPDATE remaining-1
        activate DB
        alt 影响 0 行（红包已被回收）
            DB-->>Service: errRedPacketClosed, ROLLBACK
            Service-->>Client: 失败 (红包已结束，不回补 Redis)
        else 正常
            Service->>DB: 钱包入账 + 台账 + outbox，COMMIT
            DB-->>Service: OK
            Service-->>Client: 成功 (amount)
        else 其他错误
            DB-->>Service: err, ROLLBACK
            Service->>Redis: rollback Lua (LPUSH amount, HDEL uid)
            Service-->>Client: 失败 (err)
        end
        deactivate DB
    end
    deactivate Service
```

对应代码（`internal/redpacket/service.go:158-207`）：

```go
amount, err := cache.ClaimRedPacket(ctx, rp.ID, u.Id, claimedTTL)
if err != nil {
	return nil, err
}

txErr := rpDao.DB.Transaction(func(tx *gorm.DB) error {
	txDao := NewRedPacketDaoByDB(tx)
	claim := &RedPacketClaim{RedPacketID: rp.ID, UserID: u.Id, Amount: amount}
	if e := txDao.CreateClaim(claim); e != nil {
		return e
	}
	n, e := txDao.DecrRemaining(rp.ID)
	if e != nil {
		return e
	}
	if n == 0 {
		return errRedPacketClosed
	}
	if e := txDao.MarkFinishedIfDrained(rp.ID); e != nil {
		return e
	}
	if e := settleClaimTx(tx, claim.ID, u.Id, amount); e != nil {
		return e
	}
	return outbox.NewOutboxDaoByDB(tx).Insert(/* red_packet.claimed */)
})
if txErr != nil {
	if !errors.Is(txErr, errRedPacketClosed) {
		if rbErr := cache.RollbackRedPacketClaim(ctx, rp.ID, u.Id, amount); rbErr != nil {
			// （省略日志）
		}
	}
	return nil, txErr
}
```

DB 有两道兜底：

- `uk_rp_user (red_packet_id, user_id)` 唯一索引（`model.go:39-40`），Redis 的 `claimed` 被清掉以后，同一用户也插不进第二条领取记录；
- `DecrRemaining` 带条件 `remaining > 0 AND status = active`（`internal/redpacket/repo.go:50-55`），DB 的剩余份数才是权威。

失败后回补分两种。普通失败把这份金额 `LPUSH` 回去、撤掉 `claimed` 标记，让别人或本人还能再抢（`repository/cache/redpacket.go:86-90`）。红包已被回收时不能回补：剩余金额已经按 DB 退给发包人了，再塞回 Redis 等于同一笔钱发两次（`service.go:200-206`）。

### 4.2 领取和退款为什么不会把同一笔钱发两次

这是工单 B。退款事务的写法（`internal/redpacket/task.go:48-61`）：

```go
err := dao.NewDBClient(ctx).Transaction(func(tx *gorm.DB) error {
	txDao := NewRedPacketDaoByDB(tx)
	rp, err := txDao.GetForUpdate(rpID)       // 第一条语句：加锁读
	if err != nil {
		return err
	}
	if rp.Status == RedPacketStatusRefunded {
		return nil
	}
	claimed, err := txDao.SumClaimed(rpID)    // 之后才 SUM
	if err != nil {
		return err
	}
	left = rp.Total - claimed
	// （省略：MarkStatus(refunded)、settleRefundTx、outbox）
})
```

领取事务里的 `DecrRemaining` 是一条 `UPDATE red_packet ... WHERE id = ?`，会去拿同一行的 X 锁。两边谁先拿到锁，谁先走：

```mermaid
sequenceDiagram
    participant Claim as 领取事务
    participant DB as red_packet 行
    participant Refund as 退款事务

    Claim->>DB: INSERT claim (amount=a)
    Claim->>DB: UPDATE remaining-1 (拿到行锁)
    activate DB
    Refund->>DB: SELECT ... FOR UPDATE
    Note over Refund,DB: 等待行锁
    Claim->>DB: 入账 + COMMIT
    deactivate DB
    DB-->>Refund: 拿到行锁 (status=active)
    Refund->>DB: SUM(claim) (含 a)
    Refund->>DB: refunded + 退 Total-SUM，COMMIT
```

反过来，退款先拿到锁并提交，领取事务的 `UPDATE` 等到锁后发现 `status = refunded`，影响 0 行，返回 `errRedPacketClosed`，刚插入的领取记录随事务回滚。两种顺序都满足守恒式。

### 4.3 改一行就出事故：快照在什么时候建立

退款事务第一条语句是加锁读。MySQL InnoDB 在 RR 隔离级别下，普通 `SELECT` 的一致性快照在**事务里第一次普通读**时建立，加锁读不建快照、读最新已提交版本。所以 `SumClaimed` 这条普通读建快照时，已经拿到了行锁，排在它前面的领取事务都已提交，快照里能看到它们的领取记录。

假设有人“优化”一下，在 `GetForUpdate` 之前加一条普通读，比如先 `Get(rpID)` 判断是否已退款：

```text
退款: SELECT red_packet (普通读，快照在此建立)
领取: INSERT claim(a); UPDATE remaining-1; COMMIT
退款: SELECT ... FOR UPDATE (拿到锁，读到最新行)
退款: SUM(claim)            (用的是旧快照，看不到 a)
退款: 退 Total - SUM        (多退了 a)
```

领取人拿到了 a，发包人又被退了 a，平台托管账户变成负数。所以 `refundRedPacketLeft` 把 `GetForUpdate` 放在第一条，`repo.go:45-49` 的注释讲的锁串行正是以此为前提。这种“语句顺序即正确性”的约束适合用一个并发测试固定下来，见课后练习 2。

### 4.4 回补：把同一份金额原样还回去

回到工单 A。领取事务失败、且红包没有被回收时，`Claim` 调 `RollbackRedPacketClaim`（`repository/cache/redpacket.go:204-212`），执行的脚本是（`redpacket.go:86-90`）：

```lua
redis.call('LPUSH', KEYS[1], ARGV[2])   -- 金额放回 amounts 头部
redis.call('HDEL', KEYS[2], ARGV[1])    -- 撤掉该用户的 claimed 标记
return 1
```

几个细节：

- **还的是同一个金额。** 回补参数里的 `amount` 就是刚才 Lua 出队的那一份，不重新计算。LIST 里所有份额加起来仍然等于“Total - 已领总额”，守恒式在 Redis 侧也成立。
- **`LPUSH` 而不是 `RPUSH`。** 抢包从头部 `LPOP`，放回头部让这一份最先被下一次抢走，LIST 的其余顺序不受影响。份额顺序在发包时已经 `Shuffle` 过，放回哪头都不改变分布，放回头部只是让状态最接近“这次没抢过”。
- **两步在一个脚本里。** 份额和标记必须一起还：只还份额不删标记，本人再也抢不了；只删标记不还份额，LIST 比 DB 的 `remaining` 少一份。
- **撤掉标记后，本人可以再抢。** 工单 A 的用户第二次点“抢”时，Lua 的 `HEXISTS` 不再拦截，他会重新走一遍完整链路，DB 的唯一索引和 `DecrRemaining` 条件仍然是最终裁决。

不回补的那一支是 `errRedPacketClosed`。这时退款事务已经按 `Total - SUM` 把剩余金额退给发包人，并在提交后调 `ReleaseRedPacketLeft` 清 LIST（`task.go:84-86`）。清理脚本一边 `LPOP` 一边累加（`redpacket.go:69-77`），返回的剩余总额在这里被忽略，不参与退款计算。这一份如果再 `LPUSH` 回去，就会在 Redis 里凭空多出一份已经退走的钱。

`claimed` HASH 的 TTL 设成红包过期后再留 24 小时（`service.go:157`）。红包在有效期内，同一用户的去重标记一直在；过期后状态检查先拦住请求，标记再过期也无妨。

---

## 五、兜底：发包补偿和过期回收

**本节判断：退款金额永远按 DB 算，不读 Redis；同一个退款函数既做发包失败的立即补偿，也做过期回收。**

### 5.1 状态机

```mermaid
stateDiagram-v2
    [*] --> active: 发包事务提交
    active --> finished: 领取事务中 remaining 归零
    active --> refunded: 过期回收 / Redis 准备失败补偿
    expired --> refunded: 过期回收
    finished --> [*]
    refunded --> [*]
```

`finished` 只在领取事务里 DB 剩余份数归零时写入（`repo.go:57-62`），一定没有余额，所以不扫。`refunded` 在退款事务里写入，退款函数遇到它直接返回。

`expired`（`model.go:12`）是给“已过期、待回收”预留的中间态。回收扫描同时接受 `active` 和 `expired`（`repo.go:134`），所以过期时有没有先打上这个标记，都不影响回收：判断“该不该退”的依据始终是 `expire_at` 和 DB 里的已领总额，而不是状态位。

两个终态的写入点都在持有红包行锁的事务里：`finished` 跟着最后一笔 `DecrRemaining`，`refunded` 跟着 `GetForUpdate`。所以一个红包不可能既 `finished` 又被退款，也不可能先 `refunded` 再被领到最后一份。

### 5.2 发包后 Redis 写入失败

这是工单 C。钱已经进了托管，LIST 却没写进去，所有人抢都会得到“红包已抢完”。代码立即退款（`internal/redpacket/service.go:117-126`）：

```go
if e := cache.PrepareRedPacket(ctx, rp.ID, amounts, ttl+time.Hour); e != nil {
	// 钱已扣进 escrow 但红包抢不了：立即按 DB 把总额退回发包人。补偿也失败时，
	// 红包过期后回收任务按 DB 剩余金额兜底退款。
	if _, rbErr := refundRedPacketLeft(ctx, rp.ID); rbErr != nil {
		// （省略日志）
	}
	return nil, e
}
```

这次补偿走的就是过期回收用的 `refundRedPacketLeft`。此时还没有任何领取记录，`SUM` 是 0，剩余金额就是 `Total`，整笔原路退回，红包置为 `refunded`；接口仍把 Redis 的错误返回给发包人，告诉他这次发包没成功。

补偿本身也失败时，红包仍是 `active`，有效期内谁来抢都会在 Lua 处得到“红包已抢完”，不会产生任何领取记录。过期后 cron 扫到它，`Total > 已领总额`（此时为 0），按同样的逻辑整笔退回。两条路径共用一个幂等的退款函数，所以哪条先成功都可以，另一条遇到 `refunded` 直接返回。

LIST 的 TTL 比红包过期多 1 小时，给回收留出窗口（`service.go:117-118`）。

### 5.3 过期回收

cron 每 5 分钟跑一次（`initialize/cron.go:52-59`），扫描条件（`internal/redpacket/repo.go:132-138`）：

```go
err := d.DB.Where("status IN ? AND expire_at <= ?",
		[]uint{RedPacketStatusActive, RedPacketStatusExpired}, time.Now()).
	Where("total > (SELECT COALESCE(SUM(c.amount), 0) FROM red_packet_claim c WHERE c.red_packet_id = red_packet.id)").
	Limit(limit).
	Find(&out).Error
```

注意两点：

- 用 `total > 已领总额` 判断是否还有余额，而不是看 Redis 里 LIST 是否为空。Redis 被清空、过期，或者根本没写进去，DB 都能算出该退多少；
- 不写成 `status <> refunded`，否则抢完的红包每轮都要跑一次子查询（`repo.go:128-131` 的注释）。

退款提交以后才清 Redis 里剩下的份额（`task.go:84-86`）。清理失败只记日志：即使有人再从 LIST 里 `LPOP` 到一份，领取事务也会因为 `status = refunded` 影响 0 行而作废，并且不会回补。

测试 `TestExpireRefundsLeftoverFromDBWhenRedisIsEmpty`（`internal/redpacket/service_test.go:45-100`）把 Redis 回收函数替换成永远返回 0，种了四个红包：

| 红包 | 状态 | 已领 | 期望 |
|---|---|---|---|
| 1 | active，已过期 | 300 + 200 / 1000 | 退 500 |
| 2 | expired，已过期 | 100 / 800 | 退 700 |
| 3 | finished，已过期 | 500 / 500 | 不动 |
| 4 | active，未过期 | 0 / 600 | 不动 |

跑两遍 cron，断言发包人余额是 1200、退款台账 4 条（红包 1、2 各一借一贷）、Redis 清理只调用 2 次，证明退款按 DB 算、而且重跑幂等。

### 5.4 settle 消费者：幂等的再结算

`consumer.go` 订阅 `red_packet.*`，定位是“事件驱动的兜底再结算”（`internal/redpacket/consumer.go:16-19`）。三笔资金流和 outbox 在同一个事务里提交，所以事件被投递时，对应的台账已经存在，消费者走到 `adjustWallet` 会命中 `skipped` 分支，余额保持不变。at-least-once 投递带来的重复消息，也由同一条唯一键吸收。

三个处理函数的写法值得看一眼（`consumer.go:25-76`）：

- **解码失败、关键字段为 0 的消息标成 `errSettlePoisonMessage`**，直接进 DLQ，不会无限回灌；
- **领包事件不带领取记录 id**，消费者用 `(red_packet_id, user_id)` 反查 `GetClaim`，靠 `uk_rp_user` 保证只有一行。查询出错按可重试处理（比如主从延迟导致暂时看不到），查到为空才判为脏事件；
- **退款事件 `RefundTotal <= 0` 直接确认**，和 `settleRefundTx` 对 `left <= 0` 的处理口径一致。

这套结构的意义在于给结算留了异步化的接口：哪天把资金结算从同步事务里拿出来，只靠事件驱动，入账的幂等和失败分类已经在这里写好了。毒消息和退避重试的通用机制见第 05 讲。

---

## 六、故障演练 / 课堂演示

**本节判断：每个演练都只看一件事——DB 里的守恒式是否还成立。**

### 演示 1：Redis 清空后照样按 DB 退款

```bash
go test ./internal/redpacket -run TestExpireRefundsLeftoverFromDBWhenRedisIsEmpty -v
```

现场把 `GetExpired` 的条件改成只扫 `active`，再跑一次：红包 2 不退款，发包人余额从 1200 变成 500，测试失败。说明扫描范围本身就是资金正确性的一部分。

### 演示 2：拆包算法的三条规则

现场补一个最小测试，对 `SplitRedPacket` 跑一万次随机参数，断言：

```go
// 每次：len(out) == count；每份 >= 1；sum(out) == total
amounts, err := cache.SplitRedPacket(total, count)
```

再试 `SplitRedPacket(5, 5)` 和 `SplitRedPacket(4, 5)`，前者全是 1，后者返回 `ErrRedPacketSplitInvalid`。

### 演示 3：DB 拒绝后 Redis 原样回补

本地起服务，发一个 3 份的红包，用户 A 抢一次成功。然后手工删掉 Redis 侧的去重标记，模拟 `claimed` 丢失：

```bash
redis-cli DEL redpacket:{id}:claimed
redis-cli LLEN redpacket:{id}:amounts        # 记下此时是 2
```

A 换一个 `Idempotency-Key` 再抢一次：

- Lua 的 `HEXISTS` 不再拦截，`LPOP` 出队头那一份，并重新写入 A 的标记；
- 领取事务的 `CreateClaim` 撞上 `uk_rp_user`，事务回滚，接口返回错误；
- 失败分支不是 `errRedPacketClosed`，执行回补 Lua：`LLEN` 回到 2，`LRANGE` 的结果和抢之前完全一样，A 的标记也被撤掉；
- DB 里 A 仍然只有一条领取记录，`remaining` 仍是 2，A 的钱包只入账一次。

再让 B、C 各抢一次，`remaining` 归零，红包置为 `finished`，`SUM(claim) == Total`。这个演示同时验证了 4.1 的唯一索引兜底和 4.4 的回补脚本。

### 演示 4：快照时机

在 `refundRedPacketLeft` 的 `GetForUpdate` 前面加一条 `txDao.Get(rpID)`，用两个 MySQL 会话手工复现 4.3 的时序：退款会话先执行普通读，领取会话插入领取记录、扣份数并提交，退款会话再执行剩余语句，观察退款金额多出领取的那一份。注意 SQLite 测试库不能复现，单测里 `SetMaxOpenConns(1)` 把所有事务串行化了（`service_test.go:113`）。

---

## 课后练习

1. **回补路径测试。** 连本地 Redis，构造一次必然失败的领取事务（例如预先插入同一用户的领取记录），断言回补后 LIST 长度和金额序列恢复、`claimed` 里没有该用户；再让 `refundRedPacketLeft` 在 Lua 出队之后、领取事务之前提交（在测试里把这两步拆开按顺序调用），断言领取事务返回 `errRedPacketClosed`，且 LIST 不回补。
2. **领取与退款并发测试。** 在 MySQL 上起两个 goroutine，一个对最后一份执行领取事务，一个执行 `refundRedPacketLeft`，跑 1000 轮，断言每轮都有 `SUM(claim) + 退款 == Total`。再按 4.3 的方式在退款事务开头加一条普通读，确认测试能抓到。
3. **拆包分布。** 对 `SplitRedPacket(10000, 10)` 跑十万次，统计每个位置的平均金额和最大值，验证 2.1 的“期望大致相同”，并解释 `Shuffle` 前后最后一个位置的最大值分布有什么不同。
4. **定向红包。** 给红包加一个可选的接收人列表或群 id，说明校验应该放在 Lua 之前还是 DB 事务里，`Show` 的领取明细应该对谁返回。
5. **我收到的红包。** `RedPacketClaim.UserID` 上已经有单列索引（`model.go:40`），设计一个按领取人分页的查询接口，说明为什么不需要再给 `(user_id, created_at)` 建联合索引，或者在什么数据量下需要。
6. **结算异步化。** 假设把领包入账从领取事务里拿出来，只靠 `red_packet.claimed` 事件驱动，列出 5.4 的消费者要改哪些地方，守恒式在哪个时间窗口里会暂时不成立，对账应该看什么。

---

## 代码索引

| 主题 | 文件 | 入口 |
|---|---|---|
| Redis 存储结构与 Lua | `repository/cache/redpacket.go` | `prepareRedPacketScript`、`claimRedPacketScript`、`rollbackRedPacketClaimScript` |
| 二倍均值拆包 | `repository/cache/redpacket.go` | `SplitRedPacket` |
| 表结构与状态 | `internal/redpacket/model.go` | `RedPacket`、`RedPacketClaim` |
| 发包 / 抢包 / 详情 | `internal/redpacket/service.go` | `Create`、`Claim`、`Show`、`ListMine` |
| 剩余份数与扫描 | `internal/redpacket/repo.go` | `DecrRemaining`、`GetForUpdate`、`SumClaimed`、`GetExpired` |
| 三段资金流 | `internal/redpacket/settle.go` | `adjustWallet`、`settleSendTx`、`settleClaimTx`、`settleRefundTx` |
| 过期回收 | `internal/redpacket/task.go` | `RunRedPacketExpireCheck`、`refundRedPacketLeft` |
| 事件消费 | `internal/redpacket/consumer.go` | `StartSettleConsumer`、`DispatchSettleEvent` |
| 路由与护栏 | `internal/redpacket/routes.go` | `RegisterRoutes` |
| 台账与系统账户 | `internal/money/ledger_model.go`、`internal/money/ledger_repo.go` | `AccountTransaction`、`AppendTransaction` |
| 定时任务 | `initialize/cron.go` | `RunRedPacketExpireCheck` 注册 |
| 测试 | `internal/redpacket/service_test.go` | `TestSendRejectsInsufficientBalance`、`TestExpireRefundsLeftoverFromDBWhenRedisIsEmpty` |
