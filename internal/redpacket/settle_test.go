package redpacket

import (
	"context"
	"sync"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"

	conf "github.com/RedInn7/gomall/config"
	"github.com/RedInn7/gomall/internal/money"
	"github.com/RedInn7/gomall/internal/user"
	"github.com/RedInn7/gomall/repository/db/dao"
)

var settleCfgOnce sync.Once

// AES 加密余额需要非空 MoneySecret，缺配置时给最小内存配置。
func initSettleConfig() {
	settleCfgOnce.Do(func() {
		re := conf.ConfigReader{FileName: "../../config/locales/config.yaml"}
		defer func() {
			if r := recover(); r != nil {
				conf.Config = &conf.Conf{
					EncryptSecret: &conf.EncryptSecret{MoneySecret: "MoneyTestSecret16Byte"},
				}
			}
			if conf.Config != nil && conf.Config.EncryptSecret.MoneySecret == "" {
				conf.Config.EncryptSecret.MoneySecret = "MoneyTestSecret16Byte"
			}
		}()
		conf.InitConfigForTest(&re)
	})
}

func setupSQLite(t *testing.T) (*gorm.DB, func()) {
	t.Helper()
	dsn := "file:redpacket-" + t.Name() + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{SingularTable: true},
	})
	if err != nil {
		t.Skipf("sqlite 不可用（CGO 关闭？）：%v", err)
	}
	if err := db.AutoMigrate(&user.User{}, &RedPacket{}, &RedPacketClaim{}, &money.AccountTransaction{}); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
	prev := dao.SetTestDB(db)
	return db, func() { dao.SetTestDB(prev) }
}

func seedMoneyUser(t *testing.T, db *gorm.DB, name, cents string) *user.User {
	t.Helper()
	initSettleConfig()
	u := &user.User{UserName: name, Money: cents}
	enc, err := u.EncryptMoney()
	if err != nil {
		t.Fatalf("EncryptMoney: %v", err)
	}
	u.Money = enc
	if err := db.Create(u).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	return u
}

func decBalance(t *testing.T, db *gorm.DB, id uint) int64 {
	t.Helper()
	var u user.User
	if err := db.First(&u, id).Error; err != nil {
		t.Fatalf("reload user %d: %v", id, err)
	}
	v, err := u.DecryptMoney()
	if err != nil {
		t.Fatalf("decrypt user %d: %v", id, err)
	}
	return v
}

func clearingBalance(t *testing.T, db *gorm.DB) int64 {
	t.Helper()
	var credit, debit int64
	db.Model(&money.AccountTransaction{}).
		Where("user_id=? AND direction=?", money.ExternalClearingUserID, money.DirectionCredit).
		Select("COALESCE(SUM(amount_cents),0)").Scan(&credit)
	db.Model(&money.AccountTransaction{}).
		Where("user_id=? AND direction=?", money.ExternalClearingUserID, money.DirectionDebit).
		Select("COALESCE(SUM(amount_cents),0)").Scan(&debit)
	return credit - debit
}

// TestRedPacket_SendDebitsSenderToEscrowIdempotent 发包：发包人扣 total 进平台清算 escrow，重复结算幂等。
func TestRedPacket_SendDebitsSenderToEscrowIdempotent(t *testing.T) {
	initLogForTest()
	db, cleanup := setupSQLite(t)
	defer cleanup()

	sender := seedMoneyUser(t, db, "sender", "10000")
	const rpID uint = 1
	const total int64 = 3000

	ctx := context.Background()
	if err := GetRedPacketSrv().SettleSend(ctx, rpID, sender.ID, total); err != nil {
		t.Fatalf("SettleSend: %v", err)
	}
	if got := decBalance(t, db, sender.ID); got != 7000 {
		t.Fatalf("sender after send = %d, want 7000", got)
	}
	if got := clearingBalance(t, db); got != total {
		t.Fatalf("escrow after send = %d, want %d", got, total)
	}

	// 重复结算：余额与 escrow 不变（台账唯一索引 + 预检幂等）。
	if err := GetRedPacketSrv().SettleSend(ctx, rpID, sender.ID, total); err != nil {
		t.Fatalf("SettleSend(重复): %v", err)
	}
	if got := decBalance(t, db, sender.ID); got != 7000 {
		t.Fatalf("sender after dup send = %d, want 7000", got)
	}
	if got := clearingBalance(t, db); got != total {
		t.Fatalf("escrow after dup send = %d, want %d", got, total)
	}
}

// TestRedPacket_ClaimCreditsReceiverFromEscrowIdempotent 领包：领取人入账，escrow 出账，重复结算幂等。
func TestRedPacket_ClaimCreditsReceiverFromEscrowIdempotent(t *testing.T) {
	initLogForTest()
	db, cleanup := setupSQLite(t)
	defer cleanup()

	receiver := seedMoneyUser(t, db, "receiver", "0")
	// escrow 预置一笔发包入账，模拟红包在途资金。
	if err := GetRedPacketSrv().SettleSend(context.Background(), 1, mustSeedSender(t, db), 500); err != nil {
		t.Fatalf("prep send: %v", err)
	}
	escrowBefore := clearingBalance(t, db)

	const claimID uint = 1
	const amount int64 = 200
	ctx := context.Background()
	if err := GetRedPacketSrv().SettleClaim(ctx, claimID, receiver.ID, amount); err != nil {
		t.Fatalf("SettleClaim: %v", err)
	}
	if got := decBalance(t, db, receiver.ID); got != amount {
		t.Fatalf("receiver after claim = %d, want %d", got, amount)
	}
	if got := clearingBalance(t, db); got != escrowBefore-amount {
		t.Fatalf("escrow after claim = %d, want %d", got, escrowBefore-amount)
	}

	// 同一领取记录重复结算：不二次入账。
	if err := GetRedPacketSrv().SettleClaim(ctx, claimID, receiver.ID, amount); err != nil {
		t.Fatalf("SettleClaim(重复): %v", err)
	}
	if got := decBalance(t, db, receiver.ID); got != amount {
		t.Fatalf("receiver after dup claim = %d, want %d", got, amount)
	}
}

// TestRedPacket_RefundReturnsLeftToSenderIdempotent 过期回收：剩余从 escrow 退回发包人，重复结算幂等。
func TestRedPacket_RefundReturnsLeftToSenderIdempotent(t *testing.T) {
	initLogForTest()
	db, cleanup := setupSQLite(t)
	defer cleanup()

	sender := seedMoneyUser(t, db, "sender", "10000")
	const rpID uint = 9
	const total int64 = 3000
	ctx := context.Background()
	if err := GetRedPacketSrv().SettleSend(ctx, rpID, sender.ID, total); err != nil {
		t.Fatalf("SettleSend: %v", err)
	}
	// 假设无人抢，全额回收。
	const left int64 = 3000
	if err := GetRedPacketSrv().SettleRefund(ctx, rpID, sender.ID, left); err != nil {
		t.Fatalf("SettleRefund: %v", err)
	}
	if got := decBalance(t, db, sender.ID); got != 10000 {
		t.Fatalf("sender after refund = %d, want 10000 (full return)", got)
	}
	if got := clearingBalance(t, db); got != 0 {
		t.Fatalf("escrow after full refund = %d, want 0", got)
	}

	// 重复回收：余额不变。
	if err := GetRedPacketSrv().SettleRefund(ctx, rpID, sender.ID, left); err != nil {
		t.Fatalf("SettleRefund(重复): %v", err)
	}
	if got := decBalance(t, db, sender.ID); got != 10000 {
		t.Fatalf("sender after dup refund = %d, want 10000", got)
	}
}

func mustSeedSender(t *testing.T, db *gorm.DB) uint {
	t.Helper()
	return seedMoneyUser(t, db, "escrow-src", "10000").ID
}
