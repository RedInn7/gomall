package skill

type SkillProduct struct {
	Id         uint `gorm:"primarykey"`
	ProductId  uint `gorm:"not null"`
	BossId     uint `gorm:"not null"`
	Title      string
	Money      float64
	Num        int `gorm:"not null"`
	CustomId   uint
	CustomName string
}
