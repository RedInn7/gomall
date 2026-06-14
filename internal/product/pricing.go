package product

import (
	"fmt"
	"math"
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

// ResolvePricing 反查商品并返回权威单价（分）与卖家 BossID，供需要计费的下单域使用。
// 商品不存在或定价非法时返回 error，调用方据此拒单，绝不回退到客户端金额。
func (d *ProductDao) ResolvePricing(id uint) (unitCents int64, bossID uint, err error) {
	p, perr := d.GetProductById(id)
	if perr != nil || p == nil {
		return 0, 0, fmt.Errorf("商品不存在或查询失败 product=%d: %w", id, perr)
	}
	cents, ok := p.UnitCents()
	if !ok {
		return 0, 0, fmt.Errorf("商品定价非法 product=%d discount=%q price=%q", id, p.DiscountPrice, p.Price)
	}
	return cents, p.BossID, nil
}

// ResolveBossID 反查商品卖家：仅需商品存在，不校验定价。
// 供 cart/favorite/preorder 这类只需「谁是卖家」、金额另有权威来源的场景使用。
func (d *ProductDao) ResolveBossID(id uint) (uint, error) {
	p, err := d.GetProductById(id)
	if err != nil || p == nil {
		return 0, fmt.Errorf("商品不存在或查询失败 product=%d: %w", id, err)
	}
	return p.BossID, nil
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
