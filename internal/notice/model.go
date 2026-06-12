package notice

import (
	"github.com/RedInn7/gomall/internal/shared/dbmodel"
)

// Notice 公告模型 存放公告和邮件模板
type Notice struct {
	dbmodel.Model
	Text string `gorm:"type:text"`
}
