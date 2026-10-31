package redpacket

import (
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
