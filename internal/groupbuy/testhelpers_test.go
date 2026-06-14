package groupbuy

import (
	"errors"
	"io"
	"strconv"
	"sync"
	"testing"

	"github.com/sirupsen/logrus"
	"gorm.io/gorm"

	conf "github.com/RedInn7/gomall/config"
	"github.com/RedInn7/gomall/internal/user"
	logpkg "github.com/RedInn7/gomall/pkg/utils/log"
)

func initLogForTest() {
	if logpkg.LogrusObj == nil {
		l := logrus.New()
		l.Out = io.Discard
		logpkg.LogrusObj = l
	}
}

// 资金路径（加入扣款 / 成团结算 / 散团退款）要 EncryptMoney / DecryptMoney 可逆，
// 测试只需 MoneySecret 非空即可，与 preorder 测试同套路。
var groupbuyTestConfigOnce sync.Once

func initGroupbuyTestConfig() {
	groupbuyTestConfigOnce.Do(func() {
		re := conf.ConfigReader{FileName: "../../config/locales/config.yaml"}
		defer func() {
			if r := recover(); r != nil {
				conf.Config = &conf.Conf{
					EncryptSecret: &conf.EncryptSecret{MoneySecret: "GroupbuyTestMoneySecret16Byte"},
				}
			}
			if conf.Config != nil && conf.Config.EncryptSecret.MoneySecret == "" {
				conf.Config.EncryptSecret.MoneySecret = "GroupbuyTestMoneySecret16Byte"
			}
		}()
		conf.InitConfigForTest(&re)
	})
}

// seedGroupbuyUserWithID 给指定 id 建一条带余额的用户行：加入拼团要从其钱包扣款。
// 显式指定 id 以对齐测试里硬编码的 leader / joiner userID。
func seedGroupbuyUserWithID(t *testing.T, db *gorm.DB, id uint, balanceCents int64) {
	t.Helper()
	initGroupbuyTestConfig()
	u := &user.User{UserName: "gb-user-" + strconv.FormatUint(uint64(id), 10), Money: strconv.FormatInt(balanceCents, 10)}
	u.ID = id
	enc, err := u.EncryptMoney()
	if err != nil {
		t.Fatalf("encrypt money: %v", err)
	}
	u.Money = enc
	if err := db.Create(u).Error; err != nil {
		t.Fatalf("create user %d: %v", id, err)
	}
}

// getGroupbuyUserBalance 读回用户明文余额（分），用于断言扣款 / 退款落账。
func getGroupbuyUserBalance(t *testing.T, db *gorm.DB, id uint) int64 {
	t.Helper()
	var u user.User
	if err := db.First(&u, id).Error; err != nil {
		t.Fatalf("reload user %d: %v", id, err)
	}
	bal, err := u.DecryptMoney()
	if err != nil {
		t.Fatalf("decrypt money: %v", err)
	}
	return bal
}

// safeCall 兜住 service 在 DB 未初始化时的 nil-pointer panic，让测试以 "err != nil" 形式收尾。
func safeCall(fn func() error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = errors.New("recovered panic")
		}
	}()
	return fn()
}
