package address

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"

	"github.com/RedInn7/gomall/pkg/utils/ctl"
	logpkg "github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/repository/db/dao"
	"github.com/RedInn7/gomall/types"
)

// 收货地址领域的白盒测试：sqlite in-memory 直连 dao 层。
// sqlite 不可用（CGO 关闭）时整组 skip。

func initLogForTest() {
	if logpkg.LogrusObj == nil {
		l := logrus.New()
		l.Out = io.Discard
		logpkg.LogrusObj = l
	}
}

func setupSQLiteForAddress(t *testing.T) (*gorm.DB, func()) {
	t.Helper()
	dsn := "file:address-" + t.Name() + "?mode=memory&cache=shared"
	db, err := gorm.Open(newTestDialector(dsn), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{SingularTable: true},
	})
	if err != nil {
		t.Skipf("sqlite 不可用（CGO 关闭？）：%v", err)
	}
	if err := db.AutoMigrate(&Address{}); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
	prev := dao.SetTestDB(db)
	return db, func() { dao.SetTestDB(prev) }
}

// TestAddress_CreateUpdateDeleteRoundTrip 覆盖单用户视角下的全闭环：
// 新增 -> 列表可见 -> 修改生效 -> 删除后查不到。
func TestAddress_CreateUpdateDeleteRoundTrip(t *testing.T) {
	initLogForTest()
	db, cleanup := setupSQLiteForAddress(t)
	defer cleanup()

	const uid = uint(101)
	ctx := ctl.NewContext(context.Background(), &ctl.UserInfo{Id: uid})
	srv := GetAddressSrv()

	// 新增
	if _, err := srv.AddressCreate(ctx, &AddressCreateReq{
		Name: "张三", Phone: "13800000001", Address: "上海市浦东新区张江路 1 号",
	}); err != nil {
		t.Fatalf("AddressCreate: %v", err)
	}

	var created Address
	if err := db.Where("user_id = ?", uid).First(&created).Error; err != nil {
		t.Fatalf("load created address: %v", err)
	}
	if created.Name != "张三" || created.Phone != "13800000001" {
		t.Fatalf("created row mismatch: %+v", created)
	}

	// AddressShow 返回 DataListResp，Total 与条数一致
	showResp, err := srv.AddressShow(ctx)
	if err != nil {
		t.Fatalf("AddressShow: %v", err)
	}
	listResp, ok := showResp.(*types.DataListResp)
	if !ok {
		t.Fatalf("AddressShow resp type = %T", showResp)
	}
	if listResp.Total != 1 {
		t.Fatalf("AddressShow total = %d, want 1", listResp.Total)
	}

	// 修改：电话与地址都换掉，返回值是更新后的列表
	updResp, err := srv.AddressUpdate(ctx, &AddressServiceReq{
		Id: created.ID, Name: "李四", Phone: "13900000002", Address: "北京市海淀区中关村大街 2 号",
	})
	if err != nil {
		t.Fatalf("AddressUpdate: %v", err)
	}
	updList, ok := updResp.(*types.DataListResp)
	if !ok {
		t.Fatalf("AddressUpdate resp type = %T", updResp)
	}
	items, ok := updList.Item.([]*AddressResp)
	if !ok {
		t.Fatalf("AddressUpdate item type = %T", updList.Item)
	}
	if len(items) != 1 {
		t.Fatalf("AddressUpdate items = %d, want 1", len(items))
	}
	if items[0].ID != created.ID || items[0].UserID != uid ||
		items[0].Name != "李四" || items[0].Phone != "13900000002" ||
		items[0].Address != "北京市海淀区中关村大街 2 号" {
		t.Fatalf("AddressUpdate DTO mismatch: %+v", items[0])
	}

	// 删除后按 (id, uid) 查不到
	if _, err := srv.AddressDelete(ctx, &AddressDeleteReq{Id: created.ID}); err != nil {
		t.Fatalf("AddressDelete: %v", err)
	}
	if _, err := NewAddressDaoByDB(db).GetAddressByAid(created.ID, uid); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("deleted address still readable, err = %v", err)
	}
}

// TestAddress_ListOrderedByCreatedAtDesc 验证列表按 created_at 倒序，
// 以及 DTO 的逐字段映射（含 UNIX_TIMESTAMP(created_at) AS created_at
// 的别名映射，CreatedAt 应等于落库时间的 Unix 秒）。
func TestAddress_ListOrderedByCreatedAtDesc(t *testing.T) {
	initLogForTest()
	db, cleanup := setupSQLiteForAddress(t)
	defer cleanup()

	const uid = uint(202)
	base := time.Date(2026, 1, 2, 10, 0, 0, 0, time.Local)
	names := []string{"最早", "中间", "最新"}
	for i, name := range names {
		a := &Address{
			UserID: uid, Name: name,
			Phone:   "1380000000" + string(rune('1'+i)),
			Address: "测试地址",
		}
		a.CreatedAt = base.Add(time.Duration(i) * time.Hour)
		if err := db.Create(a).Error; err != nil {
			t.Fatalf("seed address %d: %v", i, err)
		}
	}

	ctx := ctl.NewContext(context.Background(), &ctl.UserInfo{Id: uid})
	resp, err := GetAddressSrv().AddressList(ctx, &AddressListReq{})
	if err != nil {
		t.Fatalf("AddressList: %v", err)
	}
	items, ok := resp.([]*AddressResp)
	if !ok {
		t.Fatalf("AddressList resp type = %T", resp)
	}
	if len(items) != 3 {
		t.Fatalf("AddressList len = %d, want 3", len(items))
	}
	// 倒序：最新 -> 中间 -> 最早
	wantOrder := []string{"最新", "中间", "最早"}
	for i, want := range wantOrder {
		if items[i].Name != want {
			t.Fatalf("位置 %d 应为 %q，实际 %q", i, want, items[i].Name)
		}
		if items[i].UserID != uid || items[i].ID == 0 || items[i].Address != "测试地址" {
			t.Fatalf("DTO 映射缺字段: %+v", items[i])
		}
		wantTs := base.Add(time.Duration(2-i) * time.Hour).Unix()
		if items[i].CreatedAt <= 0 {
			t.Fatalf("位置 %d CreatedAt 应为正的 unix 秒，got %d", i, items[i].CreatedAt)
		}
		if items[i].CreatedAt != wantTs {
			t.Fatalf("位置 %d CreatedAt = %d, want %d", i, items[i].CreatedAt, wantTs)
		}
	}
}

// TestAddress_CrossUserIsolation 验证按 (id, user_id) 双键限定的越权防护：
// 其他用户既看不到、也删不掉别人的地址。
func TestAddress_CrossUserIsolation(t *testing.T) {
	initLogForTest()
	db, cleanup := setupSQLiteForAddress(t)
	defer cleanup()

	const owner, intruder = uint(301), uint(302)
	ownerCtx := ctl.NewContext(context.Background(), &ctl.UserInfo{Id: owner})
	intruderCtx := ctl.NewContext(context.Background(), &ctl.UserInfo{Id: intruder})
	srv := GetAddressSrv()

	if _, err := srv.AddressCreate(ownerCtx, &AddressCreateReq{
		Name: "王五", Phone: "13700000003", Address: "广州市天河区体育西路 3 号",
	}); err != nil {
		t.Fatalf("AddressCreate: %v", err)
	}
	var row Address
	if err := db.Where("user_id = ?", owner).First(&row).Error; err != nil {
		t.Fatalf("load address: %v", err)
	}

	// 他人按 (id, uid) 直查 -> not found
	if _, err := NewAddressDaoByDB(db).GetAddressByAid(row.ID, intruder); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("跨用户读取应 not found，err = %v", err)
	}

	// 他人的列表为空
	resp, err := srv.AddressShow(intruderCtx)
	if err != nil {
		t.Fatalf("AddressShow: %v", err)
	}
	if lr := resp.(*types.DataListResp); lr.Total != 0 {
		t.Fatalf("他人列表 total = %d, want 0", lr.Total)
	}

	// 他人更新不生效：where 条件带 user_id，0 行命中，owner 的数据原样保留
	if _, err := srv.AddressUpdate(intruderCtx, &AddressServiceReq{
		Id: row.ID, Name: "黑客", Phone: "13100000000", Address: "篡改地址",
	}); err != nil {
		t.Fatalf("跨用户更新不应报错（静默 0 行）: %v", err)
	}
	var afterUpdate Address
	if err := db.First(&afterUpdate, row.ID).Error; err != nil {
		t.Fatalf("reload address: %v", err)
	}
	if afterUpdate.UserID != owner || afterUpdate.Name != "王五" ||
		afterUpdate.Phone != "13700000003" || afterUpdate.Address != "广州市天河区体育西路 3 号" {
		t.Fatalf("跨用户更新不应生效: %+v", afterUpdate)
	}

	// 他人删除不生效：行依旧属于 owner
	if _, err := srv.AddressDelete(intruderCtx, &AddressDeleteReq{Id: row.ID}); err != nil {
		t.Fatalf("AddressDelete: %v", err)
	}
	if _, err := NewAddressDaoByDB(db).GetAddressByAid(row.ID, owner); err != nil {
		t.Fatalf("owner 的地址不应被他人删除：%v", err)
	}
}
