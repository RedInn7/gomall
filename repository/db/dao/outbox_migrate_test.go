package dao

import (
	"testing"

	conf "github.com/RedInn7/gomall/config"
	"github.com/RedInn7/gomall/repository/db/model"
)

func TestOutbox_Migrate(t *testing.T) {
	if _db == nil {
		re := conf.ConfigReader{FileName: "../../../config/locales/config.yaml"}
		conf.InitConfigForTest(&re)
		InitMySQL()
	}
	if err := _db.AutoMigrate(&model.OutboxEvent{}); err != nil {
		t.Fatalf("explicit AutoMigrate failed: %v", err)
	}
	if !_db.Migrator().HasTable(&model.OutboxEvent{}) {
		t.Fatal("HasTable returned false after AutoMigrate")
	}
}
