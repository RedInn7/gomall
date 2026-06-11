package notice

import (
	"context"

	"gorm.io/gorm"

	"github.com/RedInn7/gomall/repository/db/dao"
)

type NoticeDao struct {
	*gorm.DB
}

func NewNoticeDao(ctx context.Context) *NoticeDao {
	return &NoticeDao{dao.NewDBClient(ctx)}
}

func NewNoticeDaoByDB(db *gorm.DB) *NoticeDao {
	return &NoticeDao{db}
}

// GetNoticeById 通过 id 获取 notice
func (d *NoticeDao) GetNoticeById(id uint) (notice *Notice, err error) {
	err = d.DB.Model(&Notice{}).Where("id=?", id).First(&notice).Error
	return
}

// CreateNotice 创建 notice
func (d *NoticeDao) CreateNotice(notice *Notice) error {
	return d.DB.Model(&Notice{}).Create(&notice).Error
}
