package dbmodel

import "time"

// Model 各领域实体的公共基座，列布局与历史表结构保持一致
// （id 主键 + created_at / updated_at / deleted_at）。
//
// 注意：DeletedAt 是普通可空时间列，本项目当前删除语义为物理删除；
// 若需启用 gorm v2 软删除（gorm.DeletedAt 类型），属行为变更，须连同
// 唯一索引、重建流程一起评估，不在本基座默认提供。
type Model struct {
	ID        uint `gorm:"primarykey"`
	CreatedAt time.Time
	UpdatedAt time.Time
	DeletedAt *time.Time
}
