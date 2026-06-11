package order

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"gorm.io/gorm"

	"github.com/RedInn7/gomall/consts"
	"github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/repository/cache"
	"github.com/RedInn7/gomall/repository/db/dao"
)

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

// ListOrderByCondition 获取订单List
func (d *OrderDao) ListOrderByCondition(uId uint, req *OrderListReq) (r *OrderListResp, err error) {
	req.BasePage.Normalize()
	// TODO: 详情读路径目前用 join，后续考虑缓存化以支撑高并发读
	cacheKey := fmt.Sprintf("mall:orders:uid:%v:type:%v", uId, req.Type)
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
		cache.RedisClient.Set(context.Background(), cacheKey, string(bytes), 5*time.Minute)
	}

	return
}

func (d *OrderDao) ListOrderByConditionOld(uId uint, req *OrderListReq) (r *OrderListResp, count int64, err error) {
	req.BasePage.Normalize()
	// 1. 直接初始化返回对象，完全不考虑 Redis
	r = &OrderListResp{List: make([]*OrderListRespItem, 0)}

	// 2. 没有任何预防措施，直接操作 `order` 表 (连反引号都不加)
	// 没有任何动态判断，直接 Where 写死，req.Type 为 0 时也会强行查询 type=0
	query := d.DB.Table("order").Where("`order`.user_id = ? and `order`.type = ?", uId, req.Type)

	// 3. 每一页都查全表总数，200w 数据下这行代码是性能黑洞
	// 它会强迫 MySQL 进行全表扫描
	query.Count(&count)

	// 4. 使用最原始的 OFFSET 分页逻辑
	// 随着 PageNum 增大，这就是典型的深分页 (Deep Pagination)
	offset := (req.PageNum - 1) * req.PageSize

	// 5. 所有的 Join、Select 和排序全堆在一起执行
	// Order("created_at desc") 会触发文件排序 (Filesort)，因为不是主键索引
	// Offset 会让数据库数完前 N 条数据再扔掉，造成严重的磁盘 I/O 浪费
	err = query.
		Joins("left join product as p on p.id=order.product_id").
		Joins("left join address as a on a.id=order.address_id").
		Select("`order`.*, a.phone address_phone, a.address address, p.discount_price discount_price, p.img_path img_path").
		Order("order.created_at desc").
		Offset(offset).
		Limit(req.PageSize).
		Find(&r.List).Error

	if err != nil {
		log.LogrusObj.Errorf("获取订单错误，err:%v", err)
		return nil, 0, err
	}

	return r, count, nil
}

func (d *OrderDao) GetOrderById(id, uId uint) (r *Order, err error) {
	err = d.DB.Model(&Order{}).
		Where("id = ? AND user_id = ?", id, uId).
		First(&r).Error

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

// DeleteOrderById 删除订单
func (d *OrderDao) DeleteOrderById(id, uId uint) error {
	return d.DB.Model(&Order{}).
		Where("id=? AND user_id = ?", id, uId).
		Delete(&Order{}).Error
}

// UpdateOrderById 更新订单详情
func (d *OrderDao) UpdateOrderById(id, uId uint, order *Order) error {
	return d.DB.Where("id = ? AND user_id = ?", id, uId).
		Updates(order).Error
}

func (d *OrderDao) GetTimeoutOrders(minutes int, limit int) (orders []*Order, err error) {
	expireTime := time.Now().Add(-time.Duration(minutes) * time.Minute)
	err = d.DB.Model(&Order{}).Where(
		"type=? and created_at <=?", consts.UnPaid, expireTime).
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

func (d *OrderDao) CloseOrderWithCheck(orderNum uint64) (bool, error) {
	res := d.DB.Model(&Order{}).Where(
		"order_num=? and type=?", orderNum, consts.UnPaid).
		Update("type", consts.Cancelled)

	if res.Error != nil {
		return false, res.Error
	}

	return res.RowsAffected > 0, nil

}

// ShipOrder 商家发货：WaitShip -> WaitReceive。
// 条件 UPDATE 保证幂等：仅当订单仍处于 WaitShip 时才会写入；
// 已发货 / 已退款的订单 RowsAffected=0，上层据此判定非法转换。
// 物流单号本期不持久化（model 未引入字段），仅在事件层带出。
func (d *OrderDao) ShipOrder(orderNum uint64) (bool, error) {
	res := d.DB.Model(&Order{}).
		Where("order_num=? AND type=?", orderNum, consts.OrderWaitShip).
		Update("type", consts.OrderWaitReceive)
	if res.Error != nil {
		return false, res.Error
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
	return res.RowsAffected > 0, nil
}

// RequestRefund 申请退款：from 必须落在 allowedFrom 集合内（WaitShip / WaitReceive / Completed）。
// 通过 WHERE type IN (...) 一次拦截非法 from，避免读后写竞态。
func (d *OrderDao) RequestRefund(orderNum uint64, allowedFrom []uint) (bool, error) {
	if len(allowedFrom) == 0 {
		return false, nil
	}
	res := d.DB.Model(&Order{}).
		Where("order_num=? AND type IN ?", orderNum, allowedFrom).
		Update("type", consts.OrderRefunding)
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

// ApproveRefund 同意退款：Refunding -> Refunded。
func (d *OrderDao) ApproveRefund(orderNum uint64) (bool, error) {
	res := d.DB.Model(&Order{}).
		Where("order_num=? AND type=?", orderNum, consts.OrderRefunding).
		Update("type", consts.OrderRefunded)
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

// RejectRefund 驳回退款：Refunding -> Completed。
// 仅在 from=Refunding 时生效，避免误把还在 WaitShip / WaitReceive 的订单推到 Completed。
func (d *OrderDao) RejectRefund(orderNum uint64) (bool, error) {
	res := d.DB.Model(&Order{}).
		Where("order_num=? AND type=?", orderNum, consts.OrderRefunding).
		Update("type", consts.OrderCompleted)
	if res.Error != nil {
		return false, res.Error
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
