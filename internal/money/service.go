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
func (s *MoneySrv) MoneyShow(ctx context.Context, req *MoneyShowReq) (resp interface{}, err error) {
	u, err := ctl.GetUserInfo(ctx)
	if err != nil {
		log.LogrusObj.Error(err)
		return
	}
	user, err := user.NewUserDao(ctx).GetUserById(u.Id)
	if err != nil {
		log.LogrusObj.Error(err)
		return
	}
	money, err := user.DecryptMoney(req.Key)
	if err != nil {
		log.LogrusObj.Error(err)
		return
	}
	resp = &MoneyShowResp{
		UserID:    user.ID,
		UserName:  user.UserName,
		UserMoney: formatYuan(money),
	}

	return
}

func formatYuan(cents int64) string {
	sign := ""
	if cents < 0 {
		sign = "-"
		cents = -cents
	}
	return fmt.Sprintf("%s%d.%02d", sign, cents/100, cents%100)
}
