package payment

import (
	"context"
	"errors"
	"testing"

	"github.com/sirupsen/logrus"
	"gorm.io/gorm"

	conf "github.com/RedInn7/gomall/config"
	"github.com/RedInn7/gomall/consts"
	orderpkg "github.com/RedInn7/gomall/internal/order"
	"github.com/RedInn7/gomall/pkg/utils/ctl"
	util "github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/repository/db/dao"
)

func TestPayDownRejectsDepositPaidPreorder(t *testing.T) {
	db := newPaymentTestDB(t, &orderpkg.Order{})
	o := &orderpkg.Order{
		UserID: 1, BossID: 2, ProductID: 10, AddressID: 20, Num: 1, Money: 1000,
		OrderNum: 20261031001, Type: consts.OrderWaitPay, PreorderStage: 1,
	}
	if err := db.Create(o).Error; err != nil {
		t.Fatal(err)
	}
	ctx := ctl.NewContext(context.Background(), &ctl.UserInfo{Id: 1})

	_, err := GetPaymentSrv().PayDown(ctx, &PaymentDownReq{OrderId: o.ID, Key: "123456"})
	if !errors.Is(err, errPreorderNotPayable) {
		t.Fatalf("paydown on deposit-paid preorder: want errPreorderNotPayable, got %v", err)
	}
	var got orderpkg.Order
	if err := db.First(&got, o.ID).Error; err != nil {
		t.Fatal(err)
	}
	if got.Type != consts.OrderWaitPay || got.PreorderStage != 1 {
		t.Fatalf("preorder state changed: type=%d stage=%d", got.Type, got.PreorderStage)
	}
}

// newPaymentTestDB 在 newXcashTestDB 之上把库挂成全局 DB（走 dao.NewDBClient 的服务也能用），
// 并补齐余额加密密钥与日志。
func newPaymentTestDB(t *testing.T, models ...any) *gorm.DB {
	t.Helper()
	db := newXcashTestDB(t, models...)
	previousConf, previousLogger := conf.Config, util.LogrusObj
	conf.Config = &conf.Conf{EncryptSecret: &conf.EncryptSecret{MoneySecret: "payment-test-secret"}}
	util.LogrusObj = logrus.New()
	previousDB := dao.SetTestDB(db)
	t.Cleanup(func() {
		dao.SetTestDB(previousDB)
		conf.Config, util.LogrusObj = previousConf, previousLogger
	})
	return db
}
