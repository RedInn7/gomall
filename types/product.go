package types

type ProductSearchReq struct {
	ID            uint   `form:"id" json:"id"`
	Name          string `form:"name" json:"name"`
	CategoryID    int    `form:"category_id" json:"category_id"`
	Title         string `form:"title" json:"title" `
	Info          string `form:"info" json:"info" `
	Price         string `form:"price" json:"price"`
	DiscountPrice string `form:"discount_price" json:"discount_price"`
	OnSale        bool   `form:"on_sale" json:"on_sale"`
	BasePage
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
}

type ProductListReq struct {
	CategoryID uint `form:"category_id" json:"category_id"`
	BasePage
}

type ProductDeleteReq struct {
	ID uint `form:"id" json:"id"`
	BasePage
}

type ProductShowReq struct {
	ID uint `form:"id" json:"id"`
}

type ProductUpdateReq struct {
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
