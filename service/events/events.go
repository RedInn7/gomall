package events

// 业务事件 payload。routing key 与类型一一对应:
//   order.created    -> OrderCreated
//   order.paid       -> OrderPaid
//   order.cancelled  -> OrderCancelled
//   product.changed  -> ProductChanged

type OrderCreated struct {
	OrderID   uint   `json:"order_id"`
	OrderNum  uint64 `json:"order_num"`
	UserID    uint   `json:"user_id"`
	ProductID uint   `json:"product_id"`
	Num       int    `json:"num"`
}

type OrderPaid struct {
	OrderID   uint   `json:"order_id"`
	OrderNum  uint64 `json:"order_num"`
	UserID    uint   `json:"user_id"`
	ProductID uint   `json:"product_id"`
	Num       int    `json:"num"`
}

type OrderCancelled struct {
	OrderID   uint   `json:"order_id"`
	OrderNum  uint64 `json:"order_num"`
	UserID    uint   `json:"user_id"`
	ProductID uint   `json:"product_id"`
	Num       int    `json:"num"`
	Reason    string `json:"reason"`
}

type ProductChanged struct {
	ProductID uint   `json:"product_id"`
	Op        string `json:"op"` // create / update / delete
}
