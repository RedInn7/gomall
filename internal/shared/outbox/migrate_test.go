package outbox

import (
	"context"
	"testing"

	conf "github.com/RedInn7/gomall/config"
	"github.com/RedInn7/gomall/repository/db/dao"
)

func TestOutbox_Migrate(t *testing.T) {
	if dao.NewDBClient(context.Background()) == nil {
		re := conf.ConfigReader{FileName: "../../../config/locales/config.yaml"}
		conf.InitConfigForTest(&re)
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Skipf("MySQL not available: %v", r)
				}
			}()
			dao.InitMySQL()
		}()
	}
	if dao.NewDBClient(context.Background()) == nil {
		t.Skip("MySQL not initialized")
	}
	if err := dao.NewDBClient(context.Background()).AutoMigrate(&OutboxEvent{}); err != nil {
		t.Fatalf("explicit AutoMigrate failed: %v", err)
	}
	if !dao.NewDBClient(context.Background()).Migrator().HasTable(&OutboxEvent{}) {
		t.Fatal("HasTable returned false after AutoMigrate")
	}
}
