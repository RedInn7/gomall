package migrate

import (
	"testing"

	"github.com/sirupsen/logrus"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/RedInn7/gomall/internal/groupbuy"
	"github.com/RedInn7/gomall/internal/money"
	util "github.com/RedInn7/gomall/pkg/utils/log"
)

// 历史 / 滚动发布期间旧实例写到 legacy_system 的拼团流水挪到 groupbuy_escrow，
// 红包等其它系统侧流水和用户钱包流水不动；重复执行结果不变。
func TestBackfillAccountCodesMovesGroupbuyOnly(t *testing.T) {
	if util.LogrusObj == nil {
		util.LogrusObj = logrus.New()
	}
	db, err := gorm.Open(sqlite.Open("file:migrate-backfill?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&money.AccountTransaction{}); err != nil {
		t.Fatal(err)
	}
	seed := []money.AccountTransaction{
		// AutoMigrate 加列前的老行：user_id=0 被默认填成 user_wallet
		{UserID: 0, AccountCode: money.AccountCodeUserWallet, Direction: money.DirectionCredit, RefOrderID: 1, BizType: groupbuy.BizTypeGroupBuyPay},
		// 滚动发布期间旧实例写的
		{UserID: 0, AccountCode: money.AccountCodeLegacySystem, Direction: money.DirectionDebit, RefOrderID: 1, BizType: groupbuy.BizTypeGroupBuyRefund},
		{UserID: 0, AccountCode: money.AccountCodeUserWallet, Direction: money.DirectionCredit, RefOrderID: 2, BizType: "redpacket_send"},
		{UserID: 7, AccountCode: money.AccountCodeUserWallet, Direction: money.DirectionDebit, RefOrderID: 1, BizType: groupbuy.BizTypeGroupBuyPay},
	}
	if err := db.Create(&seed).Error; err != nil {
		t.Fatal(err)
	}
	want := []string{money.AccountCodeGroupbuyEscrow, money.AccountCodeGroupbuyEscrow, money.AccountCodeLegacySystem, money.AccountCodeUserWallet}
	for round := 0; round < 2; round++ {
		if err := backfillAccountCodes(db); err != nil {
			t.Fatal(err)
		}
		for i, row := range seed {
			var got money.AccountTransaction
			db.First(&got, row.ID)
			if got.AccountCode != want[i] {
				t.Fatalf("round %d row %d account_code=%s, want %s", round, i, got.AccountCode, want[i])
			}
		}
	}
}
