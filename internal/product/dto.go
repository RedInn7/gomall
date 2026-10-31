package product

import "github.com/RedInn7/gomall/types"

type ProductSearchReq struct {
	ID            uint   `form:"id" json:"id"`
	Name          string `form:"name" json:"name"`
	CategoryID    int    `form:"category_id" json:"category_id"`
	Title         string `form:"title" json:"title" `
	Info          string `form:"info" json:"info" `
	Price         string `form:"price" json:"price"`
	DiscountPrice string `form:"discount_price" json:"discount_price"`
	OnSale        bool   `form:"on_sale" json:"on_sale"`
	types.BasePage
}

type ProductCreateReq struct {
	ID            uint   `form:"id" json:"id"`
	Name          string `form:"name" json:"name"`
	CategoryID    uint   `form:"category_id" json:"category_id"`
	Title         string `form:"title" json:"title" `
	Info          string `form:"info" json:"info" `
	ImgPath       string `form:"img_path" json:"img_path"`
	Price         string `form:"price" json:"price"`
	DiscountPrice string `form:"discount_price" json:"discount_price"`
	OnSale        bool   `form:"on_sale" json:"on_sale"`
	Num           int    `form:"num" json:"num"`
	// GroupbuyPriceCents 拼团价（分），0 表示不开放拼团
	GroupbuyPriceCents int64 `form:"groupbuy_price_cents" json:"groupbuy_price_cents"`
}

type ProductListReq struct {
	CategoryID uint `form:"category_id" json:"category_id"`
	types.BasePage
}

type ProductDeleteReq struct {
	ID uint `form:"id" json:"id"`
	types.BasePage
}

type ProductShowReq struct {
	ID uint `form:"id" json:"id"`
}

// ProductUpdateReq 部分更新：字段是指针，nil 表示请求里没传、该列保持原值。
// 只传 on_sale=false 就只下架，不会把名称、价格、类目、库存写成零值。
type ProductUpdateReq struct {
	ID            uint    `form:"id" json:"id"`
	Name          *string `form:"name" json:"name"`
	CategoryID    *uint   `form:"category_id" json:"category_id"`
	Title         *string `form:"title" json:"title" `
	Info          *string `form:"info" json:"info" `
	ImgPath       string  `form:"img_path" json:"img_path"`
	Price         *string `form:"price" json:"price"`
	DiscountPrice *string `form:"discount_price" json:"discount_price"`
	OnSale        *bool   `form:"on_sale" json:"on_sale"`
	Num           *int    `form:"num" json:"num"`
	// GroupbuyPriceCents 拼团价（分），0 表示不开放拼团
	GroupbuyPriceCents *int64 `form:"groupbuy_price_cents" json:"groupbuy_price_cents"`
}

// applyTo 把请求里传了的字段写到 p 上，返回要更新的列（列名 → 新值）。
func (req *ProductUpdateReq) applyTo(p *Product) map[string]interface{} {
	updates := map[string]interface{}{}
	setIfPresent(updates, "name", &p.Name, req.Name)
	setIfPresent(updates, "category_id", &p.CategoryID, req.CategoryID)
	setIfPresent(updates, "title", &p.Title, req.Title)
	setIfPresent(updates, "info", &p.Info, req.Info)
	setIfPresent(updates, "price", &p.Price, req.Price)
	setIfPresent(updates, "discount_price", &p.DiscountPrice, req.DiscountPrice)
	setIfPresent(updates, "num", &p.Num, req.Num)
	setIfPresent(updates, "on_sale", &p.OnSale, req.OnSale)
	setIfPresent(updates, "groupbuy_price_cents", &p.GroupbuyPriceCents, req.GroupbuyPriceCents)
	return updates
}

func setIfPresent[T any](updates map[string]interface{}, column string, dst *T, v *T) {
	if v != nil {
		*dst = *v
		updates[column] = *v
	}
}

type ListProductImgReq struct {
	ID uint `json:"id" form:"id"`
}

// ProductSemanticSearchReq 语义检索请求体，CategoryID 可选过滤
type ProductSemanticSearchReq struct {
	Query      string `json:"query" form:"query" binding:"required"`
	TopK       int    `json:"top_k" form:"top_k"`
	CategoryID *uint  `json:"category_id,omitempty" form:"category_id"`
}

// ProductSemanticHit 单条命中，Score 为融合后的归一化分数
type ProductSemanticHit struct {
	Product       *ProductResp `json:"product"`
	Score         float32      `json:"score"`
	SemanticScore float32      `json:"semantic_score"`
	KeywordScore  float32      `json:"keyword_score"`
}

type ProductResp struct {
	ID            uint   `json:"id"`
	Name          string `json:"name"`
	CategoryID    uint   `json:"category_id"`
	Title         string `json:"title"`
	Info          string `json:"info"`
	ImgPath       string `json:"img_path"`
	Price         string `json:"price"`
	DiscountPrice string `json:"discount_price"`
	View          uint64 `json:"view"`
	CreatedAt     int64  `json:"created_at"`
	Num           int    `json:"num"`
	OnSale        bool   `json:"on_sale"`
	BossID        uint   `json:"boss_id"`
	BossName      string `json:"boss_name"`
	BossAvatar    string `json:"boss_avatar"`
}
