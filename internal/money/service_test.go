package money

import (
	"context"
	"io"
	"sync"
	"testing"

	"github.com/sirupsen/logrus"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"

	conf "github.com/RedInn7/gomall/config"
	"github.com/RedInn7/gomall/internal/user"
	"github.com/RedInn7/gomall/pkg/utils/ctl"
	logpkg "github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/repository/db/dao"
)

// 钱包域白盒测试：余额密文经 AES(MoneySecret + 用户支付密码) 落库，
// MoneyShow 负责解密并格式化为元。sqlite 不可用（CGO 关闭）时整组 skip。

var moneyTestConfigOnce sync.Once

// AES 需要非空 MoneySecret；优先读项目 yaml，缺失时给最小内存配置。
func initMoneyTestConfig() {
	moneyTestConfigOnce.Do(func() {
		re := conf.ConfigReader{FileName: "../../config/locales/config.yaml"}
		defer func() {
			if r := recover(); r != nil {
				conf.Config = &conf.Conf{
					EncryptSecret: &conf.EncryptSecret{
						MoneySecret: "MoneyTestSecret16Byte",
					},
				}
			}
			if conf.Config != nil && conf.Config.EncryptSecret.MoneySecret == "" {
				conf.Config.EncryptSecret.MoneySecret = "MoneyTestSecret16Byte"
			}
		}()
		conf.InitConfigForTest(&re)
	})
}

func initLogForTest() {
	if logpkg.LogrusObj == nil {
		l := logrus.New()
		l.Out = io.Discard
		logpkg.LogrusObj = l
	}
}

func setupSQLiteForMoney(t *testing.T) (*gorm.DB, func()) {
	t.Helper()
	dsn := "file:money-" + t.Name() + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{SingularTable: true},
	})
	if err != nil {
		t.Skipf("sqlite 不可用（CGO 关闭？）：%v", err)
	}
	if err := db.AutoMigrate(&user.User{}); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
	prev := dao.SetTestDB(db)
	return db, func() { dao.SetTestDB(prev) }
}

// seedUserWithMoney 写入余额密文：cents 为明文（单位分），key 为 6 位支付密码。
func seedUserWithMoney(t *testing.T, db *gorm.DB, name, cents, key string) *user.User {
	t.Helper()
	initMoneyTestConfig()
	u := &user.User{UserName: name, Money: cents}
	enc, err := u.EncryptMoney(key)
	if err != nil {
		t.Fatalf("EncryptMoney: %v", err)
	}
	u.Money = enc
	if err := db.Create(u).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	return u
}

// TestMoney_ShowDecryptsBalance 余额密文 12345 分 -> 展示 "123.45" 元。
func TestMoney_ShowDecryptsBalance(t *testing.T) {
	initLogForTest()
	db, cleanup := setupSQLiteForMoney(t)
	defer cleanup()

	const key = "123456"
	u := seedUserWithMoney(t, db, "u-money-show", "12345", key)

	ctx := ctl.NewContext(context.Background(), &ctl.UserInfo{Id: u.ID})
	resp, err := GetMoneySrv().MoneyShow(ctx, &MoneyShowReq{Key: key})
	if err != nil {
		t.Fatalf("MoneyShow: %v", err)
	}
	got, ok := resp.(*MoneyShowResp)
	if !ok {
		t.Fatalf("resp type = %T", resp)
	}
	if got.UserID != u.ID || got.UserName != "u-money-show" {
		t.Fatalf("用户信息映射不一致: %+v", got)
	}
	if got.UserMoney != "123.45" {
		t.Fatalf("user_money = %q, want %q", got.UserMoney, "123.45")
	}
}

// TestMoney_ShowWrongKeyNeverRevealsBalance 错误支付密码绝不能解出真实余额。
// 底层 AES-CBC 对错误 key 的行为是解出乱码（解析为 0）或去填充 panic，
// 两种结局都可接受，唯一不可接受的是返回正确金额。
func TestMoney_ShowWrongKeyNeverRevealsBalance(t *testing.T) {
	initLogForTest()
	db, cleanup := setupSQLiteForMoney(t)
	defer cleanup()

	u := seedUserWithMoney(t, db, "u-money-wrongkey", "12345", "123456")
	ctx := ctl.NewContext(context.Background(), &ctl.UserInfo{Id: u.ID})

	var resp interface{}
	var err error
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Logf("错误 key 触发解密 panic（已兜住）：%v", r)
			}
		}()
		resp, err = GetMoneySrv().MoneyShow(ctx, &MoneyShowReq{Key: "654321"})
	}()

	if err == nil && resp != nil {
		if got, ok := resp.(*MoneyShowResp); ok && got.UserMoney == "123.45" {
			t.Fatalf("错误 key 不应解出真实余额：%+v", got)
		}
		t.Logf("错误 key 的展示值：%+v", resp)
	}
}

// TestMoney_ShowWithoutUserInfo 未注入用户信息的 ctx 直接报错。
func TestMoney_ShowWithoutUserInfo(t *testing.T) {
	initLogForTest()
	_, cleanup := setupSQLiteForMoney(t)
	defer cleanup()

	if _, err := GetMoneySrv().MoneyShow(context.Background(), &MoneyShowReq{Key: "123456"}); err == nil {
		t.Fatal("无用户信息的 ctx 应报错")
	}
}

// TestFormatYuan 分转元的格式化边界：零值、补零、负数。
func TestFormatYuan(t *testing.T) {
	cases := []struct {
		cents int64
		want  string
	}{
		{0, "0.00"},
		{5, "0.05"},
		{100, "1.00"},
		{12345, "123.45"},
		{-130, "-1.30"},
	}
	for _, c := range cases {
		if got := formatYuan(c.cents); got != c.want {
			t.Fatalf("formatYuan(%d) = %q, want %q", c.cents, got, c.want)
		}
	}
}
