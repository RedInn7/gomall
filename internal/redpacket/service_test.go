package redpacket

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	conf "github.com/RedInn7/gomall/config"
	"github.com/RedInn7/gomall/internal/money"
	"github.com/RedInn7/gomall/internal/shared/outbox"
	"github.com/RedInn7/gomall/internal/user"
	util "github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/repository/db/dao"
)

func TestSendRejectsInsufficientBalance(t *testing.T) {
	db := openRedPacketTestDB(t)
	seedRedPacketUser(t, db, 7, 100)

	// 与 Create 同一事务形态：写红包主记录 + 发包扣款，任一失败整体回滚。
	err := db.Transaction(func(tx *gorm.DB) error {
		rp := &RedPacket{UserID: 7, Total: 500, Count: 5, Remaining: 5, ExpireAt: time.Now().Add(time.Hour), Status: RedPacketStatusActive}
		if err := NewRedPacketDaoByDB(tx).Create(rp); err != nil {
			return err
		}
		return settleSendTx(tx, rp.ID, 7, rp.Total)
	})
	if !errors.Is(err, user.ErrInsufficientBalance) {
		t.Fatalf("want ErrInsufficientBalance, got %v", err)
	}
	if got := redPacketUserBalance(t, db, 7); got != 100 {
		t.Fatalf("balance changed on failed send: got %d want 100", got)
	}
	assertRowCount(t, db, &money.AccountTransaction{}, 0)
	assertRowCount(t, db, &RedPacket{}, 0)
}

func TestExpireRefundsLeftoverFromDBWhenRedisIsEmpty(t *testing.T) {
	db := openRedPacketTestDB(t)
	seedRedPacketUser(t, db, 7, 0)
	var released []uint
	prevRelease := releaseRedPacketCache
	releaseRedPacketCache = func(_ context.Context, id uint) (int64, error) {
		released = append(released, id)
		return 0, nil // Redis 里的份额已被清空，回收不能再按它算钱
	}
	t.Cleanup(func() { releaseRedPacketCache = prevRelease })

	past, future := time.Now().Add(-time.Minute), time.Now().Add(time.Hour)
	seed := func(id uint, total int64, status uint, expireAt time.Time, claims ...int64) {
		rp := &RedPacket{UserID: 7, Total: total, Count: len(claims) + 1, Remaining: 1, ExpireAt: expireAt, Status: status}
		rp.ID = id
		if err := db.Create(rp).Error; err != nil {
			t.Fatalf("seed red packet: %v", err)
		}
		for i, amount := range claims {
			if err := db.Create(&RedPacketClaim{RedPacketID: id, UserID: uint(100 + i), Amount: amount}).Error; err != nil {
				t.Fatalf("seed claim: %v", err)
			}
		}
	}
	seed(1, 1000, RedPacketStatusActive, past, 300, 200) // 正常过期：退 500
	seed(2, 800, RedPacketStatusExpired, past, 100)      // 已标过期：同样按 DB 退 700
	seed(3, 500, RedPacketStatusFinished, past, 500)     // 真抢完：不退
	seed(4, 600, RedPacketStatusActive, future)          // 未过期：不动

	task := &RedPacketTaskService{}
	task.RunRedPacketExpireCheck()
	task.RunRedPacketExpireCheck() // 重跑幂等

	if got := redPacketUserBalance(t, db, 7); got != 1200 {
		t.Fatalf("sender refund got %d want 1200 (500 + 700)", got)
	}
	for id, want := range map[uint]uint{1: RedPacketStatusRefunded, 2: RedPacketStatusRefunded, 3: RedPacketStatusFinished, 4: RedPacketStatusActive} {
		var rp RedPacket
		if err := db.First(&rp, id).Error; err != nil {
			t.Fatalf("load red packet %d: %v", id, err)
		}
		if rp.Status != want {
			t.Fatalf("red packet %d status got %d want %d", id, rp.Status, want)
		}
	}
	var refunds int64
	if err := db.Model(&money.AccountTransaction{}).Where("biz_type = ?", money.BizTypeRedPacketRefund).Count(&refunds).Error; err != nil {
		t.Fatalf("count refund ledger: %v", err)
	}
	if refunds != 4 {
		t.Fatalf("refund ledger entries got %d want 4 (sender credit + escrow debit per packet)", refunds)
	}
	// 托管腿落在独立的 redpacket_escrow，不再混进 legacy_system。
	var escrow int64
	if err := db.Model(&money.AccountTransaction{}).
		Where("user_id = 0 AND account_code = ? AND biz_type = ?", money.AccountCodeRedPacketEscrow, money.BizTypeRedPacketRefund).
		Count(&escrow).Error; err != nil {
		t.Fatalf("count escrow ledger: %v", err)
	}
	if escrow != 2 {
		t.Fatalf("redpacket_escrow refund legs got %d want 2", escrow)
	}
	if len(released) != 2 {
		t.Fatalf("redis cleanup calls got %v want one per refunded packet", released)
	}
}

func openRedPacketTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	conf.Config = &conf.Conf{EncryptSecret: &conf.EncryptSecret{MoneySecret: "redpacket-test-secret"}}
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:redpacket-%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("unwrap sqlite: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	previous := dao.SetTestDB(db)
	previousLogger := util.LogrusObj
	util.LogrusObj = logrus.New()
	t.Cleanup(func() {
		dao.SetTestDB(previous)
		util.LogrusObj = previousLogger
		_ = sqlDB.Close()
	})
	if err := db.AutoMigrate(&user.User{}, &RedPacket{}, &RedPacketClaim{}, &money.AccountTransaction{}, &outbox.OutboxEvent{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func seedRedPacketUser(t *testing.T, db *gorm.DB, id uint, balance int64) {
	t.Helper()
	u := &user.User{UserName: fmt.Sprintf("rp-u%d", id), Money: strconv.FormatInt(balance, 10)}
	u.ID = id
	cipher, err := u.EncryptMoney()
	if err != nil {
		t.Fatalf("encrypt balance: %v", err)
	}
	u.Money = cipher
	if err := db.Create(u).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}
}

func redPacketUserBalance(t *testing.T, db *gorm.DB, id uint) int64 {
	t.Helper()
	var u user.User
	if err := db.First(&u, id).Error; err != nil {
		t.Fatalf("load user: %v", err)
	}
	balance, err := u.DecryptMoney()
	if err != nil {
		t.Fatalf("decrypt balance: %v", err)
	}
	return balance
}

func assertRowCount(t *testing.T, db *gorm.DB, model interface{}, want int64) {
	t.Helper()
	var n int64
	if err := db.Model(model).Count(&n).Error; err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if n != want {
		t.Fatalf("row count for %T: got %d want %d", model, n, want)
	}
}
