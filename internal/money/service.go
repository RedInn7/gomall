package money

import (
	"context"
	"fmt"
	"sync"

	"github.com/RedInn7/gomall/internal/user"
	"github.com/RedInn7/gomall/pkg/utils/ctl"
	"github.com/RedInn7/gomall/pkg/utils/log"
)

var MoneySrvIns *MoneySrv
var MoneySrvOnce sync.Once

type MoneySrv struct {
}

func GetMoneySrv() *MoneySrv {
	MoneySrvOnce.Do(func() {
		MoneySrvIns = &MoneySrv{}
	})
	return MoneySrvIns
}

// MoneyShow 展示用户的金额
func (s *MoneySrv) MoneyShow(ctx context.Context, req *MoneyShowReq) (*MoneyShowResp, error) {
	u, err := ctl.GetUserInfo(ctx)
	if err != nil {
		log.LogrusObj.Error(err)
		return nil, err
	}
	usr, err := user.NewUserDao(ctx).GetUserById(u.Id)
	if err != nil {
		log.LogrusObj.Error(err)
		return nil, err
	}
	// 支付密码单独校验；余额用服务端密钥解密
	if !usr.CheckMoneyPassword(req.Key) {
		log.LogrusObj.Error(user.ErrMoneyKeyIncorrect)
		return nil, user.ErrMoneyKeyIncorrect
	}
	money, err := usr.DecryptMoney()
	if err != nil {
		log.LogrusObj.Error(err)
		return nil, err
	}

	return &MoneyShowResp{
		UserID:    usr.ID,
		UserName:  usr.UserName,
		UserMoney: formatYuan(money),
	}, nil
}

func formatYuan(cents int64) string {
	sign := ""
	if cents < 0 {
		sign = "-"
		cents = -cents
	}
	return fmt.Sprintf("%s%d.%02d", sign, cents/100, cents%100)
}
