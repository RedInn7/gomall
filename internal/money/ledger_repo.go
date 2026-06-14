package money

import (
	"context"

	"gorm.io/gorm"

	"github.com/RedInn7/gomall/repository/db/dao"
)

type LedgerDao struct {
	*gorm.DB
}

func NewLedgerDao(ctx context.Context) *LedgerDao {
	return &LedgerDao{dao.NewDBClient(ctx)}
}

// NewLedgerDaoByDB 接管调用方提供的 tx，使流水写入与余额变动落在同一原子提交。
func NewLedgerDaoByDB(db *gorm.DB) *LedgerDao {
	return &LedgerDao{db}
}

// AppendTransaction 在当前事务内追加一条不可变流水。
//
// 必须与余额变动同事务调用：direction 与对手方相反，balanceAfterCents 记本次变更后余额。
// (refOrderID, direction) 唯一约束兜底，重复入账会因唯一冲突报错，由调用方回滚整个事务。
func (d *LedgerDao) AppendTransaction(userID, refOrderID uint, direction string, amountCents, balanceAfterCents int64, bizType string) error {
	tx := &AccountTransaction{
		UserID:            userID,
		Direction:         direction,
		AmountCents:       amountCents,
		RefOrderID:        refOrderID,
		BalanceAfterCents: balanceAfterCents,
		BizType:           bizType,
	}
	return d.DB.Create(tx).Error
}
