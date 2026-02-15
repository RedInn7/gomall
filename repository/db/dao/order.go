package dao

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/RedInn7/gomall/consts"
	"github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/repository/cache"
	"gorm.io/gorm"
	"time"

	"github.com/RedInn7/gomall/repository/db/model"
	"github.com/RedInn7/gomall/types"
)

type OrderDao struct {
	*gorm.DB
}

func NewOrderDao(ctx context.Context) *OrderDao {
	return &OrderDao{NewDBClient(ctx)}
}

func NewOrderDaoByDB(db *gorm.DB) *OrderDao {
	return &OrderDao{db}
}

// CreateOrder 创建订单
func (dao *OrderDao) CreateOrder(order *model.Order) error {
	return dao.DB.Create(&order).Error
}

// ListOrderByCondition 获取订单List
func (dao *OrderDao) ListOrderByCondition(uId uint, req *types.OrderListReq) (r *types.OrderListResp, err error) {
	// TODO 商城算是一个TOC的应用，TOC的应该是不允许join操作的，看看后续怎么改走缓存，比如走缓存，找找免费的CDN之类的
	cacheKey := fmt.Sprintf("mall:orders:uid:%v:type:%v", uId, req.Type)
	if req.LastId == 0 {
		val, err := cache.RedisClient.Get(context.Background(), cacheKey).Result()
		if err == nil && val != "" {
			r = &types.OrderListResp{List: make([]*types.OrderListRespItem, 0)}
			if jsonErr := json.Unmarshal([]byte(val), r); jsonErr == nil {
				return r, nil
			}
		}
	}

	r = &types.OrderListResp{List: make([]*types.OrderListRespItem, 0)}
	baseQuery := dao.DB.Table("`order` as o").Where("o.user_id = ? and o.type=?", uId, req.Type)

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

func (dao *OrderDao) GetOrderById(id, uId uint) (r *model.Order, err error) {
	err = dao.DB.Model(&model.Order{}).
		Where("id = ? AND user_id = ?", id, uId).
		First(&r).Error

	return
}

// ShowOrderById 获取订单详情
func (dao *OrderDao) ShowOrderById(id, uId uint) (r *types.OrderListRespItem, err error) {
	err = dao.DB.Model(&model.Order{}).
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

// DeleteOrderById 获取订单详情
func (dao *OrderDao) DeleteOrderById(id, uId uint) error {
	return dao.DB.Model(&model.Order{}).
		Where("id=? AND user_id = ?", id, uId).
		Delete(&model.Order{}).Error
}

// UpdateOrderById 更新订单详情
func (dao *OrderDao) UpdateOrderById(id, uId uint, order *model.Order) error {
	return dao.DB.Where("id = ? AND user_id = ?", id, uId).
		Updates(order).Error
}

func (dao *OrderDao) GetTimeoutOrders(minutes int, limit int) (orders []*model.Order, err error) {
	expireTime := time.Now().Add(-time.Duration(minutes) * time.Minute)
	err = dao.DB.Model(&model.Order{}).Where(
		"type=? and created_at <=?", consts.UnPaid, expireTime).
		Limit(limit).
		Find(&orders).Error

	return
}

func (dao *OrderDao) CloseOrderWithCheck(orderNum uint64) (bool, error) {
	res := dao.DB.Model(&model.Order{}).Where(
		"order_num=? and type=?", orderNum, consts.UnPaid).
		Update("type", consts.Cancelled)

	if res.Error != nil {
		return false, res.Error
	}

	return res.RowsAffected > 0, nil

}

func NewOrderDaoWithDB(db *gorm.DB) *OrderDao {
	return &OrderDao{DB: db}
}
