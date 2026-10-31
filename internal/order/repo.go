package order

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/RedInn7/gomall/consts"
	"github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/repository/cache"
	"github.com/RedInn7/gomall/repository/db/dao"
)

// orderListCacheTTL 订单列表缓存基础 TTL。
const orderListCacheTTL = 5 * time.Minute

// orderListCacheJitter 列表缓存 TTL 的最大正向抖动，打散同批写入的过期时刻，避免缓存雪崩。
const orderListCacheJitter = 60 * time.Second

// orderListCacheKey 列表缓存 key，按用户 + type 分桶。
func orderListCacheKey(uID uint, typ interface{}) string {
	return fmt.Sprintf("mall:orders:uid:%v:type:%v", uID, typ)
}

// invalidateUserOrderListCache 删除某用户的全部订单列表分桶缓存。
// 订单状态变更后列表内容会跨 type 桶迁移（如 WaitPay->WaitShip），旧桶仍缓存着已迁走的订单，
// 必须把该用户所有 type 桶一并失效，否则用户最长 TTL 内仍看到陈旧状态 / 重复订单。
// 桶按已知订单状态枚举删除，避免 KEYS/SCAN 全库扫描。
func invalidateUserOrderListCache(uID uint) {
	if uID == 0 || cache.RedisClient == nil {
		return
	}
	types := []uint{
		consts.OrderWaitPay, consts.OrderWaitShip, consts.OrderClosed,
		consts.OrderWaitReceive, consts.OrderCompleted, consts.OrderRefunding,
		consts.OrderRefunded, consts.OrderWaitGroup,
	}
	keys := make([]string, 0, len(types))
	for _, t := range types {
		keys = append(keys, orderListCacheKey(uID, t))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := cache.RedisClient.Del(ctx, keys...).Err(); err != nil {
		log.LogrusObj.Errorf("订单列表缓存失效失败 uid=%d err=%v", uID, err)
	}
}

// resolveOrderUserID 取订单归属用户，供按 order_num 推进状态的方法在失效列表缓存时定位用户桶。
func (d *OrderDao) resolveOrderUserID(orderNum uint64) uint {
	var o Order
	if err := d.DB.Model(&Order{}).Select("user_id").Where("order_num=?", orderNum).First(&o).Error; err != nil {
		log.LogrusObj.Errorf("订单列表缓存失效定位用户失败 order_num=%d err=%v", orderNum, err)
		return 0
	}
	return o.UserID
}

type OrderDao struct {
	*gorm.DB
}

func NewOrderDao(ctx context.Context) *OrderDao {
	return &OrderDao{dao.NewDBClient(ctx)}
}

func NewOrderDaoByDB(db *gorm.DB) *OrderDao {
	return &OrderDao{db}
}

// CreateOrder 创建订单
func (d *OrderDao) CreateOrder(order *Order) error {
	return d.DB.Create(&order).Error
}

// UpdatePromoFields 满减预算耗尽降级时，把订单上的满减字段改回无折扣。
// 仅在下单事务里使用：调用方已持有 tx；不要在事务外调用，否则违反 FinalCents 与
// PromoDiscountCents 必须同进同退的约束。
func (d *OrderDao) UpdatePromoFields(orderID, ruleID uint, discountCents, finalCents int64) error {
	return d.DB.Model(&Order{}).
		Where("id = ?", orderID).
		Updates(map[string]interface{}{
			"promo_rule_id":        ruleID,
			"promo_discount_cents": discountCents,
			"final_cents":          finalCents,
		}).Error
}

// SumReservedNumByProduct 汇总每个商品仍持有预占的订单数量(Σ Num)：WaitPay 未支付单（含已付定金的
// 预售单），以及 WaitGroup 拼团中订单（参团已收款，成团时才核销预占）。
// 这是 Redis reserved 桶的对账基准：DB 订单是真相，reserved 不该超过这个口径，
// 超出的部分即为崩溃在「写 Redis、提交 DB」之间留下的孤儿预占。
func (d *OrderDao) SumReservedNumByProduct() (map[uint]int64, error) {
	return d.sumNumByProduct("type IN ?", []uint{consts.OrderWaitPay, consts.OrderWaitGroup})
}

// SumCommitPendingNumByProduct 汇总每个商品「已支付、Redis 预占还没核销」的数量(Σ Num)：
//   - type=WaitShip 且 updated_at 在 since 之后：刚由支付推进而来，核销还在途；
//   - StockCommitPending：支付时重试核销仍失败、等对账补核销的订单，不论已推进到什么状态。
//
// 宽限期只取新近订单，避免把早已核销完成的 WaitShip 也算进基准而漏掉真实泄漏。
func (d *OrderDao) SumCommitPendingNumByProduct(since time.Time) (map[uint]int64, error) {
	return d.sumNumByProduct("(type = ? AND updated_at >= ?) OR stock_commit_pending = ?",
		consts.OrderWaitShip, since, true)
}

func (d *OrderDao) sumNumByProduct(query string, args ...interface{}) (map[uint]int64, error) {
	var rows []struct {
		ProductID uint
		Total     int64
	}
	if err := d.DB.Model(&Order{}).
		Select("product_id, SUM(num) AS total").
		Where(query, args...).
		Group("product_id").
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	m := make(map[uint]int64, len(rows))
	for _, r := range rows {
		m[r.ProductID] = r.Total
	}
	return m, nil
}

// ListStockCommitPending 拉一批预占待补核销的订单，供库存对账补核销。
func (d *OrderDao) ListStockCommitPending(limit int) (orders []*Order, err error) {
	err = d.DB.Model(&Order{}).Where("stock_commit_pending = ?", true).Limit(limit).Find(&orders).Error
	return
}

// SetStockCommitPending 条件翻转待核销标记，返回是否真的翻转了。
// 补核销前先翻成 false 抢占，多实例对账时同一份预占只会被一个实例核销。
func (d *OrderDao) SetStockCommitPending(orderID uint, pending bool) (bool, error) {
	res := d.DB.Model(&Order{}).
		Where("id = ? AND stock_commit_pending = ?", orderID, !pending).
		Update("stock_commit_pending", pending)
	return res.RowsAffected > 0, res.Error
}

// ListOrderByCondition 获取订单List
func (d *OrderDao) ListOrderByCondition(uId uint, req *OrderListReq) (r *OrderListResp, err error) {
	req.BasePage.Normalize()
	// TODO: 详情读路径目前用 join，后续考虑缓存化以支撑高并发读
	cacheKey := orderListCacheKey(uId, req.Type)
	if req.LastId == 0 {
		val, err := cache.RedisClient.Get(context.Background(), cacheKey).Result()
		if err == nil && val != "" {
			r = &OrderListResp{List: make([]*OrderListRespItem, 0)}
			if jsonErr := json.Unmarshal([]byte(val), r); jsonErr == nil {
				return r, nil
			}
		}
	}

	r = &OrderListResp{List: make([]*OrderListRespItem, 0)}
	baseQuery := d.DB.Table("`order` as o").Where("o.user_id = ? and o.type=?", uId, req.Type)

	if req.LastId > 0 {
		baseQuery = baseQuery.Where("o.id<?", req.LastId)
	}
	baseQuery = baseQuery.Order("o.id desc").Limit(req.PageSize)

	err = baseQuery.Joins("left join product as p on p.id=o.product_id").
		Joins("left join address as a on a.id=o.address_id").
		Select("o.*,a.phone address_phone,a.address address,p.discount_price discount_price,p.img_path img_path").
		Find(&r.List).Error

	if err != nil {
		log.LogrusObj.Errorf("获取订单错误，err:%v", err)
		return nil, err
	}
	if len(r.List) > 0 {
		r.LastId = int(r.List[len(r.List)-1].ID)
	}

	if req.LastId == 0 {
		bytes, _ := json.Marshal(r)
		ttl := orderListCacheTTL + time.Duration(rand.Int63n(int64(orderListCacheJitter)))
		cache.RedisClient.Set(context.Background(), cacheKey, string(bytes), ttl)
	}

	return
}

func (d *OrderDao) GetOrderById(id, uId uint) (r *Order, err error) {
	err = d.DB.Model(&Order{}).
		Where("id = ? AND user_id = ?", id, uId).
		First(&r).Error

	return
}

// GetOrderByIdOnly 仅按订单 id 查询，不限定 user_id。
// 用于链上确认结算等无用户上下文的场景：order 来源由调用方（如 listener 监听到的合约事件）保证。
func (d *OrderDao) GetOrderByIdOnly(id uint) (r *Order, err error) {
	err = d.DB.Model(&Order{}).Where("id = ?", id).First(&r).Error
	return
}

// ShowOrderById 获取订单详情
func (d *OrderDao) ShowOrderById(id, uId uint) (r *OrderListRespItem, err error) {
	err = d.DB.Model(&Order{}).
		Joins("AS o LEFT JOIN product AS p ON p.id = o.product_id").
		Joins("LEFT JOIN address AS a ON a.id = o.address_id").
		Where("o.id = ? AND o.user_id = ?", id, uId).
		Select("o.id AS id," +
			"o.order_num AS order_num," +
			"UNIX_TIMESTAMP(o.created_at) AS created_at," +
			"UNIX_TIMESTAMP(o.updated_at) AS updated_at," +
			"o.user_id AS user_id," +
			"o.product_id AS product_id," +
			"o.boss_id AS boss_id," +
			"o.num AS num," +
			"o.type AS type," +
			"p.name AS name," +
			"p.discount_price AS discount_price," +
			"p.img_path AS img_path," +
			"a.name AS address_name," +
			"a.phone AS address_phone," +
			"a.address AS address").
		Find(&r).Error

	return
}

// DeleteOrderById 删除买家自己的订单，返回是否真的删掉了一行。
// DeletedAt 是普通可空列不是 gorm.DeletedAt，这里是物理 DELETE：订单一删，托管款、清算单和
// 结算消费者都找不到它，所以只删资金已闭环的终态单——
//   - Closed：没收过款，或收的款已原路退回；
//   - Completed / Refunded：清算单已 settled / refunded，或根本没有清算单（迁移前旧单、预售 / 拼团单）。
//
// 条件写进 DELETE 的 WHERE，与并发的状态推进 / 结算竞争时也不会误删。
func (d *OrderDao) DeleteOrderById(id, uId uint) (bool, error) {
	res := d.DB.Model(&Order{}).
		Where("id=? AND user_id = ?", id, uId).
		Where("(type=? OR (type IN ? AND NOT EXISTS (SELECT 1 FROM payment_clearing WHERE order_id=? AND status NOT IN ?)))",
			consts.OrderClosed, []uint{consts.OrderCompleted, consts.OrderRefunded}, id,
			// 即 clearing.StatusSettled / StatusRefunded；clearing 依赖 order，这里不能反向 import。
			[]string{"settled", "refunded"}).
		Delete(&Order{})
	if res.Error != nil {
		return false, res.Error
	}
	if res.RowsAffected > 0 {
		invalidateUserOrderListCache(uId)
	}
	return res.RowsAffected > 0, nil
}

// UpdateOrderById 更新订单详情
func (d *OrderDao) UpdateOrderById(id, uId uint, order *Order) error {
	res := d.DB.Where("id = ? AND user_id = ?", id, uId).
		Updates(order)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected > 0 {
		invalidateUserOrderListCache(uId)
	}
	return nil
}

// notPreorderSQL 普通订单条件。预售单付完定金后 type 仍是 WaitPay（preorder_stage=1），
// 但它不是"未支付订单"：定金已经划给卖家，超时关单会把定金吞掉，普通支付入口会按全款再收一次。
// 预售单只能走 preorder 包自己的尾款 / 取消 / 没收流程。
const notPreorderSQL = "preorder_stage=0"

// MarkOrderPaidWithCheck 条件推进订单状态 WaitPay -> WaitShip。
// 把状态守卫塞进 WHERE：只有仍处于 WaitPay 的普通订单才会被改为 WaitShip。
// 并发支付 / 支付与取消竞争时，只有第一个事务影响 1 行，其余 RowsAffected=0，
// 上层据此回滚，杜绝重复支付。返回 ok=true 表示本次推进成功。
func (d *OrderDao) MarkOrderPaidWithCheck(orderID, userID uint) (bool, error) {
	res := d.DB.Model(&Order{}).
		Where("id=? AND user_id=? AND type=?", orderID, userID, consts.OrderWaitPay).
		Where(notPreorderSQL).
		Update("type", consts.OrderWaitShip)
	if res.Error != nil {
		return false, res.Error
	}
	if res.RowsAffected > 0 {
		invalidateUserOrderListCache(userID)
	}
	return res.RowsAffected > 0, nil
}

// CountUnpaidByUser 用户名下未支付的普通订单数（已付定金的预售单不算，见 notPreorderSQL）。
func (d *OrderDao) CountUnpaidByUser(userID uint) (n int64, err error) {
	err = d.DB.Model(&Order{}).
		Where("user_id = ? AND type = ?", userID, consts.OrderWaitPay).
		Where(notPreorderSQL).
		Count(&n).Error
	return
}

// GetTimeoutOrders 拉取超时未支付的普通订单，供 cron 兜底关单。预售单不在其列（见 notPreorderSQL）。
func (d *OrderDao) GetTimeoutOrders(minutes int, limit int) (orders []*Order, err error) {
	expireTime := time.Now().Add(-time.Duration(minutes) * time.Minute)
	err = d.DB.Model(&Order{}).Where(
		"type=? and created_at <=?", consts.OrderWaitPay, expireTime).
		Where(notPreorderSQL).
		Limit(limit).
		Find(&orders).Error

	return
}

// GetOrderByOrderNum 通过 order_num 查询订单
func (d *OrderDao) GetOrderByOrderNum(orderNum uint64) (*Order, error) {
	var o Order
	err := d.DB.Model(&Order{}).Where("order_num=?", orderNum).First(&o).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &o, nil
}

// GetOrderByTicket 按异步下单 ticket 查订单，不存在返回 nil。
func (d *OrderDao) GetOrderByTicket(ticket string) (*Order, error) {
	var o Order
	err := d.DB.Model(&Order{}).Where("ticket = ?", ticket).First(&o).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &o, nil
}

// GetOrderByOrderNumForUpdate 在事务内锁住订单，供退款等“读取当前状态后再迁移”的流程使用。
func (d *OrderDao) GetOrderByOrderNumForUpdate(orderNum uint64) (*Order, error) {
	var o Order
	err := d.DB.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("order_num = ?", orderNum).First(&o).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &o, nil
}

// CloseOrderWithCheck 关闭未支付订单：WaitPay -> Closed。cron 超时扫描与 MQ 延迟关单共用。
// 已付定金的预售单不关（见 notPreorderSQL），RowsAffected=0 由上层按幂等 no-op 处理。
func (d *OrderDao) CloseOrderWithCheck(orderNum uint64) (bool, error) {
	res := d.DB.Model(&Order{}).Where(
		"order_num=? and type=?", orderNum, consts.OrderWaitPay).
		Where(notPreorderSQL).
		Update("type", consts.OrderClosed)

	if res.Error != nil {
		return false, res.Error
	}
	if res.RowsAffected > 0 {
		invalidateUserOrderListCache(d.resolveOrderUserID(orderNum))
	}
	return res.RowsAffected > 0, nil

}

// whereBoss 商家写操作的归属条件：bossID=0 表示 admin，不限归属；否则只命中该商家自己的订单。
func whereBoss(q *gorm.DB, bossID uint) *gorm.DB {
	if bossID == 0 {
		return q
	}
	return q.Where("boss_id=?", bossID)
}

// ShipOrder 商家发货：WaitShip -> WaitReceive。
// 条件 UPDATE 保证幂等：仅当订单仍处于 WaitShip 时才会写入；
// 已发货 / 已退款的订单 RowsAffected=0，上层据此判定非法转换。
// bossID 见 whereBoss：商家只能发自己的货。
// 物流单号本期不持久化（model 未引入字段），仅在事件层带出。
func (d *OrderDao) ShipOrder(orderNum uint64, bossID uint) (bool, error) {
	res := whereBoss(d.DB.Model(&Order{}), bossID).
		Where("order_num=? AND type=?", orderNum, consts.OrderWaitShip).
		Update("type", consts.OrderWaitReceive)
	if res.Error != nil {
		return false, res.Error
	}
	if res.RowsAffected > 0 {
		invalidateUserOrderListCache(d.resolveOrderUserID(orderNum))
	}
	return res.RowsAffected > 0, nil
}

// ConfirmReceive 确认收货：WaitReceive -> Completed。
// 用户主动确认与 7d 兜底 cron 共用同一条 SQL，依靠 WHERE 兜底幂等。
func (d *OrderDao) ConfirmReceive(orderNum uint64) (bool, error) {
	res := d.DB.Model(&Order{}).
		Where("order_num=? AND type=?", orderNum, consts.OrderWaitReceive).
		Update("type", consts.OrderCompleted)
	if res.Error != nil {
		return false, res.Error
	}
	if res.RowsAffected > 0 {
		invalidateUserOrderListCache(d.resolveOrderUserID(orderNum))
	}
	return res.RowsAffected > 0, nil
}

// RequestRefund 申请退款：from 必须落在 allowedFrom 集合内（WaitShip / WaitReceive / Completed）。
// 通过 WHERE type IN (...) 一次拦截非法 from，避免读后写竞态。
func (d *OrderDao) RequestRefund(orderNum uint64, fromType uint) (bool, error) {
	res := d.DB.Model(&Order{}).
		Where("order_num=? AND type=?", orderNum, fromType).
		Updates(map[string]interface{}{"type": consts.OrderRefunding, "refund_from_type": fromType})
	if res.Error != nil {
		return false, res.Error
	}
	if res.RowsAffected > 0 {
		invalidateUserOrderListCache(d.resolveOrderUserID(orderNum))
	}
	return res.RowsAffected > 0, nil
}

// ApproveRefund 同意退款：Refunding -> Refunded。bossID 见 whereBoss。
func (d *OrderDao) ApproveRefund(orderNum uint64, bossID uint) (bool, error) {
	res := whereBoss(d.DB.Model(&Order{}), bossID).
		Where("order_num=? AND type=?", orderNum, consts.OrderRefunding).
		Update("type", consts.OrderRefunded)
	if res.Error != nil {
		return false, res.Error
	}
	if res.RowsAffected > 0 {
		invalidateUserOrderListCache(d.resolveOrderUserID(orderNum))
	}
	return res.RowsAffected > 0, nil
}

// RejectRefund 驳回退款：Refunding -> 申请前状态。
// restoreType 来自进入退款时持久化的 RefundFromType，不能一律跳到 Completed 绕过履约。
// bossID 见 whereBoss。
func (d *OrderDao) RejectRefund(orderNum uint64, restoreType uint, bossID uint) (bool, error) {
	res := whereBoss(d.DB.Model(&Order{}), bossID).
		Where("order_num=? AND type=?", orderNum, consts.OrderRefunding).
		Update("type", restoreType)
	if res.Error != nil {
		return false, res.Error
	}
	if res.RowsAffected > 0 {
		invalidateUserOrderListCache(d.resolveOrderUserID(orderNum))
	}
	return res.RowsAffected > 0, nil
}

// GetTimeoutWaitReceive 拉取已发货超过 days 天仍未确认收货的订单，由 cron 兜底确认收货。
// 仅扫 WaitReceive 状态，避免误碰退款流程。
func (d *OrderDao) GetTimeoutWaitReceive(days int, limit int) (orders []*Order, err error) {
	if days <= 0 {
		days = 7
	}
	if limit <= 0 {
		limit = 100
	}
	expireTime := time.Now().Add(-time.Duration(days) * 24 * time.Hour)
	err = d.DB.Model(&Order{}).
		Where("type=? AND updated_at <=?", consts.OrderWaitReceive, expireTime).
		Limit(limit).
		Find(&orders).Error
	return
}
