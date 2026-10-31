package product

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// 商品定价与卖家归属是计费/打款的权威来源。各下单域（order/groupbuy/preorder/cart/...）
// 一律从这里反查，禁止信客户端传入的 money/boss_id —— 否则等于把定价权与收款方交给买家。

// UnitCents 返回商品的权威单价（分）：优先 DiscountPrice，回退 Price。
// 商品价以两位小数的「元」字符串存储（如 "99.50"），与订单的「分」口径不同，统一在此换算。
// 解析失败或非正返回 ok=false，由调用方拒单或兜底。
func (p *Product) UnitCents() (int64, bool) {
	if c, ok := yuanToCents(p.DiscountPrice); ok && c > 0 {
		return c, true
	}
	if c, ok := yuanToCents(p.Price); ok && c > 0 {
		return c, true
	}
	return 0, false
}

// GroupbuyMinPriceBps 拼团价相对商品零售价的最低占比（基点，10000=100%）。
// 拼团价由商家在商品上配置，写入与开团时都服务端封顶 + 兜底：上界=零售价（不得高于零售价），
// 下界=零售价 * 该比例（默认 50%，挡住误配成 1 分钱）。运营要更低折扣调此常量。
const GroupbuyMinPriceBps = 5000

// ErrGroupbuyNotOpen 商品未开放拼团（GroupbuyPriceCents=0）。
var ErrGroupbuyNotOpen = errors.New("该商品未开放拼团")

// ErrProductNotOnSale 商品已下架：下单、预售、拼团一律拒绝。
var ErrProductNotOnSale = errors.New("商品已下架")

// checkGroupbuyPrice 校验商家配置的拼团价：0 表示不开放拼团；
// 否则须落在 [零售价 * GroupbuyMinPriceBps / 10000, 零售价]。
func (p *Product) checkGroupbuyPrice() error {
	if p.GroupbuyPriceCents == 0 {
		return nil
	}
	retail, ok := p.UnitCents()
	if !ok {
		return fmt.Errorf("商品定价非法 discount=%q price=%q，无法设置拼团价", p.DiscountPrice, p.Price)
	}
	floor := retail * GroupbuyMinPriceBps / 10000
	if p.GroupbuyPriceCents < floor || p.GroupbuyPriceCents > retail {
		return fmt.Errorf("拼团价 %d 分越界，须落在 [%d, %d] 分（商品零售价 %d 分）", p.GroupbuyPriceCents, floor, retail, retail)
	}
	return nil
}

// ResolveGroupbuyPricing 反查商品的拼团价（分）与卖家 BossID，供拼团开团使用。
// 价格只认商家配置的 GroupbuyPriceCents，绝不信客户端；未开放拼团，或配置已越界
// （比如零售价绕过商品接口被改低）时返回 error，调用方据此拒单。
func (d *ProductDao) ResolveGroupbuyPricing(id uint) (priceCents int64, bossID uint, err error) {
	p, perr := d.GetProductById(id)
	if perr != nil || p == nil {
		return 0, 0, fmt.Errorf("商品不存在或查询失败 product=%d: %w", id, perr)
	}
	if p.GroupbuyPriceCents == 0 {
		return 0, 0, ErrGroupbuyNotOpen
	}
	if !p.OnSale {
		return 0, 0, ErrProductNotOnSale
	}
	if err = p.checkGroupbuyPrice(); err != nil {
		return 0, 0, err
	}
	return p.GroupbuyPriceCents, p.BossID, nil
}

// ResolveBossID 反查商品卖家：商品须存在且在售，不校验定价。
// 供预售付定金、参团这类只需「谁是卖家」、金额另有权威来源的下单场景使用。
func (d *ProductDao) ResolveBossID(id uint) (uint, error) {
	p, err := d.GetProductById(id)
	if err != nil || p == nil {
		return 0, fmt.Errorf("商品不存在或查询失败 product=%d: %w", id, err)
	}
	if !p.OnSale {
		return 0, ErrProductNotOnSale
	}
	return p.BossID, nil
}

// pricePattern 商品价的格式：「元」，最多两位小数，如 "99"、"99.5"、"99.50"。
var pricePattern = regexp.MustCompile(`^\d{1,9}(\.\d{1,2})?$`)

// checkPrices 校验更新请求里传了的价格：零售价须大于 0；折后价可传空串表示取消折扣。
func (req *ProductUpdateReq) checkPrices() error {
	if req.Price != nil {
		if c, _ := yuanToCents(*req.Price); !pricePattern.MatchString(*req.Price) || c == 0 {
			return fmt.Errorf("价格 %q 格式不对，应为大于 0、最多两位小数的金额（元）", *req.Price)
		}
	}
	if req.DiscountPrice != nil && *req.DiscountPrice != "" && !pricePattern.MatchString(*req.DiscountPrice) {
		return fmt.Errorf("折后价 %q 格式不对，应为最多两位小数的金额（元），传空串表示取消折扣", *req.DiscountPrice)
	}
	return nil
}

// yuanToCents 把「元」字符串转成「分」。解析失败或为负返回 ok=false。
func yuanToCents(s string) (int64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	yuan, err := strconv.ParseFloat(s, 64)
	if err != nil || yuan < 0 {
		return 0, false
	}
	// Round 抵消二进制浮点误差（99.50*100 可能算出 9949.999...）。
	return int64(math.Round(yuan * 100)), true
}
