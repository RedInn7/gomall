package redpacket

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin/binding"
	"gorm.io/gorm"

	"github.com/RedInn7/gomall/internal/money"
	"github.com/RedInn7/gomall/internal/user"
	"github.com/RedInn7/gomall/pkg/utils/ctl"
	"github.com/RedInn7/gomall/repository/cache"
)

const testPin = "135790"

func asUser(uid uint) context.Context {
	return ctl.NewContext(context.Background(), &ctl.UserInfo{Id: uid})
}

// seedSender 发包人：有余额、设了支付密码
func seedSender(t *testing.T, db *gorm.DB, id uint, balance int64) {
	t.Helper()
	seedRedPacketUser(t, db, id, balance)
	u := &user.User{}
	if err := u.SetMoneyPassword(testPin); err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&user.User{}).Where("id = ?", id).Update("money_password_digest", u.MoneyPasswordDigest).Error; err != nil {
		t.Fatal(err)
	}
}

func createPacket(t *testing.T, sender uint, total int64, count int) *RedPacketResp {
	t.Helper()
	resp, err := GetRedPacketSrv().Create(asUser(sender), &RedPacketCreateReq{Total: total, Count: count, Key: testPin, Greeting: "恭喜发财"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	return resp.(*RedPacketResp)
}

// escrowBalance 红包托管账户余额 = SUM(credit) - SUM(debit)，所有资金落定后应为 0。
func escrowBalance(t *testing.T, db *gorm.DB) int64 {
	t.Helper()
	var credit, debit int64
	q := func(dir string, out *int64) {
		if err := db.Model(&money.AccountTransaction{}).
			Where("user_id = 0 AND account_code = ? AND direction = ?", money.AccountCodeRedPacketEscrow, dir).
			Select("COALESCE(SUM(amount_cents),0)").Scan(out).Error; err != nil {
			t.Fatal(err)
		}
	}
	q(money.DirectionCredit, &credit)
	q(money.DirectionDebit, &debit)
	return credit - debit
}

func sumClaims(t *testing.T, db *gorm.DB, rpID uint) int64 {
	t.Helper()
	n, err := NewRedPacketDaoByDB(db).SumClaimed(rpID)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// 30 人并发抢 10 份、每人连点两次：只有 10 人到账，钱包入账总额 = 红包总额，托管清零，红包置 finished。
func TestConcurrentClaimsConserveMoney(t *testing.T) {
	db := openRedPacketTestDB(t)
	seedSender(t, db, 1, 1000)
	for uid := uint(100); uid < 130; uid++ {
		seedRedPacketUser(t, db, uid, 0)
	}
	rp := createPacket(t, 1, 1000, 10)
	if rp.ShareCode == "" {
		t.Fatal("create should return share code to sender")
	}

	var ok, drained, dup int64
	var wg sync.WaitGroup
	start := make(chan struct{})
	for uid := uint(100); uid < 130; uid++ {
		for k := 0; k < 2; k++ {
			wg.Add(1)
			go func(uid uint) {
				defer wg.Done()
				<-start
				_, err := GetRedPacketSrv().Claim(asUser(uid), &RedPacketClaimReq{ID: rp.ID, Code: rp.ShareCode})
				switch {
				case err == nil:
					atomic.AddInt64(&ok, 1)
				case errors.Is(err, ErrRedPacketDrained), errors.Is(err, ErrRedPacketEnded):
					atomic.AddInt64(&drained, 1)
				case errors.Is(err, ErrRedPacketAlreadyTaken):
					atomic.AddInt64(&dup, 1)
				default:
					t.Errorf("claim uid=%d: %v", uid, err)
				}
			}(uid)
		}
	}
	close(start)
	wg.Wait()

	if ok != 10 {
		t.Fatalf("successful claims %d want 10 (drained=%d dup=%d)", ok, drained, dup)
	}
	var received int64
	for uid := uint(100); uid < 130; uid++ {
		received += redPacketUserBalance(t, db, uid)
	}
	if received != 1000 || sumClaims(t, db, rp.ID) != 1000 {
		t.Fatalf("received %d claims %d want 1000", received, sumClaims(t, db, rp.ID))
	}
	if got := escrowBalance(t, db); got != 0 {
		t.Fatalf("escrow balance %d want 0", got)
	}
	var row RedPacket
	db.First(&row, rp.ID)
	if row.Status != RedPacketStatusFinished || row.Remaining != 0 {
		t.Fatalf("packet status=%d remaining=%d", row.Status, row.Remaining)
	}
}

// 领取和过期退款并发：不管谁先拿到红包行锁，领走的 + 退回的 = 总额，托管清零，不会同一份钱发两次。
// 设 REDPACKET_TEST_MYSQL_DSN 时按 InnoDB 真行锁 / RR 快照跑，守住 refundRedPacketLeft 里 FOR UPDATE 必须打头的前提。
func TestClaimAndRefundRaceConservesMoney(t *testing.T) {
	db := openRedPacketTestDB(t)
	seedSender(t, db, 1, 100000)
	for uid := uint(100); uid < 120; uid++ {
		seedRedPacketUser(t, db, uid, 0)
	}
	for round := 0; round < 5; round++ {
		rp := createPacket(t, 1, 2000, 20)
		var wg sync.WaitGroup
		start := make(chan struct{})
		for uid := uint(100); uid < 120; uid++ {
			wg.Add(1)
			go func(uid uint) {
				defer wg.Done()
				<-start
				_, _ = GetRedPacketSrv().Claim(asUser(uid), &RedPacketClaimReq{ID: rp.ID, Code: rp.ShareCode})
			}(uid)
		}
		var refunded int64
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			time.Sleep(time.Duration(round) * time.Millisecond)
			left, err := refundRedPacketLeft(context.Background(), rp.ID)
			if err != nil {
				t.Errorf("refund: %v", err)
			}
			refunded = left
		}()
		close(start)
		wg.Wait()

		if claimed := sumClaims(t, db, rp.ID); claimed+refunded != 2000 {
			t.Fatalf("round %d: claimed %d + refunded %d != 2000", round, claimed, refunded)
		}
	}
	var received int64
	for uid := uint(100); uid < 120; uid++ {
		received += redPacketUserBalance(t, db, uid)
	}
	if got := redPacketUserBalance(t, db, 1) + received; got != 100000 {
		t.Fatalf("sender + receivers = %d want 100000", got)
	}
	if got := escrowBalance(t, db); got != 0 {
		t.Fatalf("escrow balance %d want 0", got)
	}
}

// DB 事务确定失败（领取人钱包不存在）：Saga 回滚把同一份放回 LIST、撤掉标记。
func TestClaimTxFailureRollsBackRedis(t *testing.T) {
	db := openRedPacketTestDB(t)
	seedSender(t, db, 1, 500)
	rp := createPacket(t, 1, 500, 1)

	if _, err := GetRedPacketSrv().Claim(asUser(404), &RedPacketClaimReq{ID: rp.ID, Code: rp.ShareCode}); err == nil {
		t.Fatal("claim by user without wallet should fail")
	}
	marks, _ := cache.ListRedPacketClaimed(context.Background(), rp.ID)
	if _, ok := marks[404]; ok {
		t.Fatal("claimed mark should be rolled back")
	}
	if exists, _ := cache.RedPacketPoolExists(context.Background(), rp.ID); !exists {
		t.Fatal("share should be pushed back")
	}
	seedRedPacketUser(t, db, 405, 0)
	if _, err := GetRedPacketSrv().Claim(asUser(405), &RedPacketClaimReq{ID: rp.ID, Code: rp.ShareCode}); err != nil {
		t.Fatalf("next claimer should get the restored share: %v", err)
	}
	if got := redPacketUserBalance(t, db, 405); got != 500 {
		t.Fatalf("balance %d want 500", got)
	}
}

// COMMIT 成功但客户端收到错误：不能回补 Redis（否则多出一份、托管多付），按成功返回。
func TestClaimCommitUnknownDoesNotRollback(t *testing.T) {
	db := openRedPacketTestDB(t)
	seedSender(t, db, 1, 300)
	seedRedPacketUser(t, db, 100, 0)
	seedRedPacketUser(t, db, 101, 0)
	rp := createPacket(t, 1, 300, 2)

	var rollbacks int64
	prevTx, prevRb := doClaimTx, rollbackClaimCache
	doClaimTx = func(db *gorm.DB, rpID, uid uint, amount int64) (*RedPacketClaim, error) {
		if _, err := claimTx(db, rpID, uid, amount); err != nil {
			return nil, err
		}
		return nil, errors.New("invalid connection") // 提交已成功，回包丢了
	}
	rollbackClaimCache = func(ctx context.Context, id, userID uint, amount int64) error {
		atomic.AddInt64(&rollbacks, 1)
		return prevRb(ctx, id, userID, amount)
	}
	t.Cleanup(func() { doClaimTx, rollbackClaimCache = prevTx, prevRb })

	resp, err := GetRedPacketSrv().Claim(asUser(100), &RedPacketClaimReq{ID: rp.ID, Code: rp.ShareCode})
	if err != nil {
		t.Fatalf("committed claim should be reported as success: %v", err)
	}
	if rollbacks != 0 {
		t.Fatalf("rollback called %d times on committed claim", rollbacks)
	}
	doClaimTx = prevTx
	if _, err := GetRedPacketSrv().Claim(asUser(101), &RedPacketClaimReq{ID: rp.ID, Code: rp.ShareCode}); err != nil {
		t.Fatal(err)
	}
	// 只有两份：两人领完后 Redis 里不能多出一份
	if exists, _ := cache.RedPacketPoolExists(context.Background(), rp.ID); exists {
		t.Fatal("redis has an extra share after a committed claim was rolled back")
	}
	got := resp.(*RedPacketClaimResp).Amount + redPacketUserBalance(t, db, 101)
	if got != 300 || escrowBalance(t, db) != 0 {
		t.Fatalf("claimed total %d escrow %d", got, escrowBalance(t, db))
	}
}

// 崩溃在 Lua 和 DB 之间：claimed 有标记、DB 没记录。对账任务按标记补领入账，并发跑多次也只补一次；
// 还在在途窗口内的标记不动。
func TestReconcileRepairsOrphanClaim(t *testing.T) {
	db := openRedPacketTestDB(t)
	seedSender(t, db, 1, 1000)
	seedRedPacketUser(t, db, 100, 0)
	seedRedPacketUser(t, db, 101, 0)
	rp := createPacket(t, 1, 1000, 3)
	ctx := context.Background()

	old, err := cache.ClaimRedPacket(ctx, rp.ID, 100, time.Hour) // 抢到后进程崩溃
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cache.ClaimRedPacket(ctx, rp.ID, 101, time.Hour); err != nil { // 刚抢到，DB 事务还在跑
		t.Fatal(err)
	}
	ageMark(t, rp.ID, 100, old)

	// 用户再点：Redis 说领过了，钱还没到
	if _, err := GetRedPacketSrv().Claim(asUser(100), &RedPacketClaimReq{ID: rp.ID, Code: rp.ShareCode}); !errors.Is(err, ErrRedPacketAlreadyTaken) {
		t.Fatalf("want already taken got %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			(&RedPacketTaskService{}).RunRedPacketClaimReconcile()
		}()
	}
	wg.Wait()

	if got := redPacketUserBalance(t, db, 100); got != old {
		t.Fatalf("orphan repaired balance %d want %d", got, old)
	}
	if got := redPacketUserBalance(t, db, 101); got != 0 {
		t.Fatalf("in-flight claim must not be touched, balance %d", got)
	}
	if c, _ := NewRedPacketDaoByDB(db).GetClaim(rp.ID, 101); c != nil {
		t.Fatal("in-flight claim must not be repaired")
	}
	var row RedPacket
	db.First(&row, rp.ID)
	if row.Remaining != 2 {
		t.Fatalf("remaining %d want 2", row.Remaining)
	}
	if escrowBalance(t, db) != 1000-old {
		t.Fatalf("escrow %d want %d", escrowBalance(t, db), 1000-old)
	}

	// 管理端对单个红包再跑一次：没有新孤儿，幂等
	resp, err := GetRedPacketSrv().AdminReconcile(ctx, &RedPacketReconcileReq{ID: rp.ID})
	if err != nil || resp.(*RedPacketReconcileResp).Repaired != 0 {
		t.Fatalf("admin reconcile %+v %v", resp, err)
	}
}

// 过期回收前先补孤儿：抢到的那份给领取人，剩下的才退发包人。
func TestExpireRepairsOrphanBeforeRefund(t *testing.T) {
	db := openRedPacketTestDB(t)
	seedSender(t, db, 1, 1000)
	seedRedPacketUser(t, db, 100, 0)
	rp := createPacket(t, 1, 1000, 2)
	ctx := context.Background()
	amt, err := cache.ClaimRedPacket(ctx, rp.ID, 100, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	ageMark(t, rp.ID, 100, amt)
	db.Model(&RedPacket{}).Where("id = ?", rp.ID).Update("expire_at", time.Now().Add(-time.Minute))

	(&RedPacketTaskService{}).RunRedPacketExpireCheck()

	if got := redPacketUserBalance(t, db, 100); got != amt {
		t.Fatalf("receiver %d want %d", got, amt)
	}
	if got := redPacketUserBalance(t, db, 1); got != 1000-amt {
		t.Fatalf("sender refund %d want %d", got, 1000-amt)
	}
	if escrowBalance(t, db) != 0 {
		t.Fatalf("escrow %d", escrowBalance(t, db))
	}
}

// 份额 LIST 丢了（发包后写 Redis 失败且补偿也失败 / Redis 丢数据）：不等过期，对账立即按 DB 退款。
func TestReconcileRefundsWhenPoolMissing(t *testing.T) {
	db := openRedPacketTestDB(t)
	seedSender(t, db, 1, 1000)
	lost := createPacket(t, 1, 600, 3)
	kept := createPacket(t, 1, 400, 2)
	ctx := context.Background()
	cache.RedisClient.Del(ctx, cache.RedPacketAmountsKey(lost.ID))
	db.Model(&RedPacket{}).Where("id IN ?", []uint{lost.ID, kept.ID}).Update("created_at", time.Now().Add(-time.Hour))

	(&RedPacketTaskService{}).RunRedPacketClaimReconcile()

	var a, b RedPacket
	db.First(&a, lost.ID)
	db.First(&b, kept.ID)
	if a.Status != RedPacketStatusRefunded || b.Status != RedPacketStatusActive {
		t.Fatalf("status lost=%d kept=%d", a.Status, b.Status)
	}
	if got := redPacketUserBalance(t, db, 1); got != 600 {
		t.Fatalf("sender %d want 600", got)
	}
}

// 口令：没有口令领不了、看不了明细；领过的人和发包人不用口令也能看。
func TestShareCodeGuardsClaimAndShow(t *testing.T) {
	db := openRedPacketTestDB(t)
	seedSender(t, db, 1, 500)
	seedRedPacketUser(t, db, 100, 0)
	seedRedPacketUser(t, db, 200, 0)
	rp := createPacket(t, 1, 500, 2)
	srv := GetRedPacketSrv()

	if _, err := srv.Claim(asUser(100), &RedPacketClaimReq{ID: rp.ID}); !errors.Is(err, ErrRedPacketForbidden) {
		t.Fatalf("claim without code want forbidden got %v", err)
	}
	if _, err := srv.Claim(asUser(100), &RedPacketClaimReq{ID: rp.ID, Code: "0000000000000000"}); !errors.Is(err, ErrRedPacketForbidden) {
		t.Fatalf("claim with wrong code want forbidden got %v", err)
	}
	if _, err := srv.Show(asUser(200), &RedPacketShowReq{ID: rp.ID}); !errors.Is(err, ErrRedPacketForbidden) {
		t.Fatalf("stranger show want forbidden got %v", err)
	}
	if _, err := srv.Claim(asUser(100), &RedPacketClaimReq{ID: rp.ID, Code: rp.ShareCode}); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		uid  uint
		code string
		see  bool // 能看到口令
	}{{100, "", false}, {200, rp.ShareCode, false}, {1, "", true}} {
		resp, err := srv.Show(asUser(c.uid), &RedPacketShowReq{ID: rp.ID, Code: c.code})
		if err != nil {
			t.Fatalf("show uid=%d: %v", c.uid, err)
		}
		d := resp.(*RedPacketDetailResp)
		if len(d.Claims) != 1 || (d.RedPacket.ShareCode != "") != c.see {
			t.Fatalf("show uid=%d: %+v", c.uid, d.RedPacket)
		}
	}

	// 迁移前的老红包没有口令，领取按老规则放行
	legacy := &RedPacket{UserID: 1, Total: 1, Count: 1, Remaining: 1, ExpireAt: time.Now().Add(time.Hour), Status: RedPacketStatusActive}
	db.Create(legacy)
	_ = cache.PrepareRedPacket(context.Background(), legacy.ID, []int64{1}, time.Hour)
	if _, err := srv.Claim(asUser(200), &RedPacketClaimReq{ID: legacy.ID}); err != nil {
		t.Fatalf("legacy claim: %v", err)
	}
}

func TestListReceived(t *testing.T) {
	db := openRedPacketTestDB(t)
	seedSender(t, db, 1, 1000)
	seedRedPacketUser(t, db, 100, 0)
	var ids []uint
	for i := 0; i < 3; i++ {
		rp := createPacket(t, 1, 100, 1)
		ids = append(ids, rp.ID)
		if _, err := GetRedPacketSrv().Claim(asUser(100), &RedPacketClaimReq{ID: rp.ID, Code: rp.ShareCode}); err != nil {
			t.Fatal(err)
		}
	}
	page1, err := GetRedPacketSrv().ListReceived(asUser(100), &RedPacketListReq{PageSize: 2})
	if err != nil {
		t.Fatal(err)
	}
	p1 := page1.(*RedPacketReceivedResp)
	if len(p1.List) != 2 || p1.List[0].RedPacketID != ids[2] || p1.List[0].SenderID != 1 || p1.List[0].Amount != 100 || p1.List[0].Greeting != "恭喜发财" {
		t.Fatalf("page1 %+v", p1.List[0])
	}
	page2, _ := GetRedPacketSrv().ListReceived(asUser(100), &RedPacketListReq{PageSize: 2, LastID: p1.LastID})
	if p2 := page2.(*RedPacketReceivedResp); len(p2.List) != 1 || p2.List[0].RedPacketID != ids[0] {
		t.Fatalf("page2 %+v", p2.List)
	}
	if other, _ := GetRedPacketSrv().ListReceived(asUser(1), &RedPacketListReq{}); len(other.(*RedPacketReceivedResp).List) != 0 {
		t.Fatal("sender received nothing")
	}
}

func TestGreetingLengthValidated(t *testing.T) {
	ok := &RedPacketCreateReq{Total: 1, Count: 1, Key: "x", Greeting: strings.Repeat("福", 128)}
	if err := binding.Validator.ValidateStruct(ok); err != nil {
		t.Fatalf("128 chars should pass: %v", err)
	}
	ok.Greeting += "福"
	if err := binding.Validator.ValidateStruct(ok); err == nil {
		t.Fatal("129 chars should be rejected before entering the tx")
	}
}

// 事件到了但台账缺腿：消费者按事件补记（并告警），重复投递不再动钱。
func TestSettleConsumerRepairsMissingLedger(t *testing.T) {
	db := openRedPacketTestDB(t)
	seedRedPacketUser(t, db, 7, 500)
	payload := []byte(`{"red_packet_id":42,"user_id":7,"total":200}`)
	for i := 0; i < 2; i++ {
		if err := DispatchSettleEvent(context.Background(), "red_packet.created", payload); err != nil {
			t.Fatal(err)
		}
	}
	if got := redPacketUserBalance(t, db, 7); got != 300 {
		t.Fatalf("balance %d want 300", got)
	}
	if escrowBalance(t, db) != 200 {
		t.Fatalf("escrow %d want 200", escrowBalance(t, db))
	}
}

// ageMark 把领取标记的时间改到在途窗口之前，模拟「抢到后很久都没落库」。
func ageMark(t *testing.T, rpID, uid uint, amount int64) {
	t.Helper()
	v := strconv.FormatInt(amount, 10) + ":" + strconv.FormatInt(time.Now().Add(-10*time.Minute).Unix(), 10)
	if err := cache.RedisClient.HSet(context.Background(), cache.RedPacketClaimedKey(rpID), strconv.FormatUint(uint64(uid), 10), v).Err(); err != nil {
		t.Fatal(err)
	}
}
