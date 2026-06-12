package skill

import (
	"context"
	"io"
	"testing"

	"github.com/sirupsen/logrus"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"

	logpkg "github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/repository/db/dao"
)

// 秒杀域 repo 层的 DB 闭环测试：sqlite in-memory，覆盖单条/批量写入与
// 在售（num > 0）过滤。Service 层（InitSkillGoods / ListSkillGoods /
// GetSkillGoods / SkillProduct 下单）全部以 Redis 为热路径，不在本组覆盖。

func setupSQLiteForSkill(t *testing.T) (*gorm.DB, func()) {
	t.Helper()
	dsn := "file:skill-" + t.Name() + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{SingularTable: true},
	})
	if err != nil {
		t.Skipf("sqlite 不可用（CGO 关闭？）：%v", err)
	}
	if err := db.AutoMigrate(&SkillProduct{}); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
	prev := dao.SetTestDB(db)
	return db, func() {
		dao.SetTestDB(prev)
	}
}

func TestSkillGoodsDao_CreateAndLoad(t *testing.T) {
	initLogForTest()
	db, cleanup := setupSQLiteForSkill(t)
	defer cleanup()

	in := &SkillProduct{ProductId: 11, BossId: 2, Title: "限时秒杀", Money: 199.0, Num: 10}
	if err := NewSkillGoodsDao(context.Background()).Create(in); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if in.Id == 0 {
		t.Fatal("主键应当回填")
	}

	var got SkillProduct
	if err := db.First(&got, in.Id).Error; err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.ProductId != 11 || got.BossId != 2 || got.Num != 10 || got.Money != 199.0 {
		t.Fatalf("row = %+v", got)
	}
}

func TestSkillGoodsDao_BatchCreate(t *testing.T) {
	initLogForTest()
	db, cleanup := setupSQLiteForSkill(t)
	defer cleanup()

	in := make([]*SkillProduct, 0, 9)
	for i := 1; i <= 9; i++ {
		in = append(in, &SkillProduct{
			ProductId: uint(i), BossId: 2, Title: "秒杀商品", Money: 200, Num: 10,
		})
	}
	if err := NewSkillGoodsDao(context.Background()).BatchCreate(in); err != nil {
		t.Fatalf("BatchCreate: %v", err)
	}

	var cnt int64
	db.Model(&SkillProduct{}).Count(&cnt)
	if cnt != 9 {
		t.Fatalf("rows = %d, want 9", cnt)
	}
	for i, sp := range in {
		if sp.Id == 0 {
			t.Fatalf("第 %d 条主键未回填", i)
		}
	}
}

func TestSkillGoodsDao_CreateByList(t *testing.T) {
	initLogForTest()
	db, cleanup := setupSQLiteForSkill(t)
	defer cleanup()

	in := []*SkillProduct{
		{ProductId: 21, BossId: 3, Title: "a", Money: 100, Num: 5},
		{ProductId: 22, BossId: 3, Title: "b", Money: 120, Num: 6},
	}
	if err := NewSkillGoodsDao(context.Background()).CreateByList(in); err != nil {
		t.Fatalf("CreateByList: %v", err)
	}
	var cnt int64
	db.Model(&SkillProduct{}).Count(&cnt)
	if cnt != 2 {
		t.Fatalf("rows = %d, want 2", cnt)
	}
}

func TestSkillGoodsDao_ListSkillGoods_FiltersSoldOut(t *testing.T) {
	initLogForTest()
	db, cleanup := setupSQLiteForSkill(t)
	defer cleanup()

	seed := []*SkillProduct{
		{ProductId: 31, BossId: 2, Title: "在售A", Money: 100, Num: 3},
		{ProductId: 32, BossId: 2, Title: "售罄", Money: 100, Num: 0},
		{ProductId: 33, BossId: 2, Title: "在售B", Money: 100, Num: 7},
	}
	if err := db.Create(&seed).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}

	got, err := NewSkillGoodsDao(context.Background()).ListSkillGoods()
	if err != nil {
		t.Fatalf("ListSkillGoods: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expect 2 on-sale rows, got %d", len(got))
	}
	for _, sp := range got {
		if sp.Num <= 0 {
			t.Fatalf("售罄商品不应出现在列表：%+v", sp)
		}
	}
}

func initLogForTest() {
	if logpkg.LogrusObj == nil {
		l := logrus.New()
		l.Out = io.Discard
		logpkg.LogrusObj = l
	}
}
