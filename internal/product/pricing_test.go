package product

import (
	"errors"
	"testing"
)

func TestCheckGroupbuyPriceBounds(t *testing.T) {
	tests := []struct {
		name     string
		discount string
		cents    int64
		wantErr  bool
	}{
		{"未开放拼团", "", 0, false},
		{"下限 50%", "", 5000, false},
		{"等于零售价", "", 10000, false},
		{"低于下限", "", 4999, true},
		{"高于零售价", "", 10001, true},
		{"负数", "", -1, true},
		{"零售价以折后价为准", "80.00", 8001, true},
	}
	for _, tt := range tests {
		p := &Product{Price: "100.00", DiscountPrice: tt.discount, GroupbuyPriceCents: tt.cents}
		if err := p.checkGroupbuyPrice(); (err != nil) != tt.wantErr {
			t.Fatalf("%s: err=%v wantErr=%v", tt.name, err, tt.wantErr)
		}
	}
}

func TestUpdateReqCheckPrices(t *testing.T) {
	tests := []struct {
		name     string
		price    *string
		discount *string
		wantErr  bool
	}{
		{"都没传", nil, nil, false},
		{"两位小数", new("99.50"), new("88"), false},
		{"取消折扣", nil, new(""), false},
		{"零售价为 0", new("0.00"), nil, true},
		{"零售价为空", new(""), nil, true},
		{"三位小数", new("9.999"), nil, true},
		{"科学计数法", new("1e3"), nil, true},
		{"负数", nil, new("-1"), true},
	}
	for _, tt := range tests {
		req := &ProductUpdateReq{Price: tt.price, DiscountPrice: tt.discount}
		if err := req.checkPrices(); (err != nil) != tt.wantErr {
			t.Fatalf("%s: err=%v wantErr=%v", tt.name, err, tt.wantErr)
		}
	}
}

func TestResolveGroupbuyPricingOnlyTrustsMerchantPrice(t *testing.T) {
	db := openProductOutboxTestDB(t, false)
	productDao := NewProductDaoByDB(db)

	closed := &Product{Name: "closed", Price: "100.00", BossID: 22}
	open := &Product{Name: "open", Price: "100.00", BossID: 22, GroupbuyPriceCents: 8000, OnSale: true}
	if err := db.Create([]*Product{closed, open}).Error; err != nil {
		t.Fatalf("seed products: %v", err)
	}

	if _, _, err := productDao.ResolveGroupbuyPricing(closed.ID); !errors.Is(err, ErrGroupbuyNotOpen) {
		t.Fatalf("未开放拼团应拒绝, got %v", err)
	}
	price, boss, err := productDao.ResolveGroupbuyPricing(open.ID)
	if err != nil || price != 8000 || boss != 22 {
		t.Fatalf("应返回商家配置的拼团价: price=%d boss=%d err=%v", price, boss, err)
	}

	// 零售价绕过商品接口被改低后，原拼团价已高于零售价，开团必须拒绝
	if err := db.Model(open).Update("price", "60.00").Error; err != nil {
		t.Fatal(err)
	}
	if _, _, err := productDao.ResolveGroupbuyPricing(open.ID); err == nil {
		t.Fatal("拼团价越界时应拒绝开团")
	}
}
