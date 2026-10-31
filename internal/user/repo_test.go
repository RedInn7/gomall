package user

import (
	"fmt"
	"strconv"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	conf "github.com/RedInn7/gomall/config"
	"github.com/RedInn7/gomall/repository/db/dao"
)

func TestProfileUpdateDoesNotOverwriteConcurrentBalanceChange(t *testing.T) {
	db := openUserTestDB(t)
	seedUserWithBalance(t, db, 1, 1000)
	userDao := NewUserDaoByDB(db)

	// 资料更新路径不加锁，先读到旧行（余额 1000）。
	stale, err := userDao.GetUserById(1)
	if err != nil {
		t.Fatalf("load stale user: %v", err)
	}

	// 资金路径在 FOR UPDATE 事务里把余额扣到 700 并提交。
	if err := db.Transaction(func(tx *gorm.DB) error {
		txDao := NewUserDaoByDB(tx)
		u, err := txDao.GetUserByIdForUpdate(1)
		if err != nil {
			return err
		}
		u.Money = "700"
		if u.Money, err = u.EncryptMoney(); err != nil {
			return err
		}
		return txDao.UpdateUserMoney(1, u.Money)
	}); err != nil {
		t.Fatalf("debit balance: %v", err)
	}

	// 资料更新带着旧行回写。
	stale.NickName = "renamed"
	if err := userDao.UpdateUserById(1, stale); err != nil {
		t.Fatalf("update profile: %v", err)
	}

	var got User
	if err := db.First(&got, 1).Error; err != nil {
		t.Fatalf("reload user: %v", err)
	}
	balance, err := got.DecryptMoney()
	if err != nil {
		t.Fatalf("decrypt balance: %v", err)
	}
	if balance != 700 {
		t.Fatalf("profile update overwrote balance: got %d want 700", balance)
	}
	if got.NickName != "renamed" {
		t.Fatalf("nick name not updated: %q", got.NickName)
	}
}

func openUserTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	conf.Config = &conf.Conf{EncryptSecret: &conf.EncryptSecret{MoneySecret: "user-test-secret"}}
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:user-%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("unwrap sqlite: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	previous := dao.SetTestDB(db)
	t.Cleanup(func() {
		dao.SetTestDB(previous)
		_ = sqlDB.Close()
	})
	if err := db.AutoMigrate(&User{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func seedUserWithBalance(t *testing.T, db *gorm.DB, id uint, balance int64) *User {
	t.Helper()
	u := &User{UserName: fmt.Sprintf("user-%d", id), Money: strconv.FormatInt(balance, 10)}
	u.ID = id
	cipher, err := u.EncryptMoney()
	if err != nil {
		t.Fatalf("encrypt balance: %v", err)
	}
	u.Money = cipher
	if err := db.Create(u).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}
	return u
}
