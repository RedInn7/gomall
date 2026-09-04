package clearing

import "time"

// PaymentAnomalyListFilter 是管理员异常款列表的分页和筛选条件。
type PaymentAnomalyListFilter struct {
	Page     int
	PageSize int
	Status   string
	Channel  string
	Reason   string
	OrderID  uint
}

type PaymentAnomalyListItem struct {
	ID                uint       `json:"id"`
	OrderID           uint       `json:"order_id"`
	Channel           string     `json:"channel"`
	ProviderRef       string     `json:"provider_ref"`
	ProviderAmount    string     `json:"provider_amount"`
	Currency          string     `json:"currency"`
	Reason            string     `json:"reason"`
	Status            string     `json:"status"`
	OccurredAt        time.Time  `json:"occurred_at"`
	LastOperatorID    uint       `json:"last_operator_id,omitempty"`
	LastActionAt      *time.Time `json:"last_action_at,omitempty"`
	LastNote          string     `json:"last_note,omitempty"`
	ExternalRefundRef string     `json:"external_refund_reference,omitempty"`
	ResolvedAt        *time.Time `json:"resolved_at,omitempty"`
}

type PaymentAnomalyTransitionItem struct {
	ID                uint      `json:"id"`
	FromStatus        string    `json:"from_status"`
	ToStatus          string    `json:"to_status"`
	OperatorID        uint      `json:"operator_id"`
	Note              string    `json:"note"`
	ExternalRefundRef string    `json:"external_refund_reference,omitempty"`
	ActedAt           time.Time `json:"acted_at"`
}

type PaymentAnomalyListResponse struct {
	Items    []PaymentAnomalyListItem `json:"items"`
	Total    int64                    `json:"total"`
	Page     int                      `json:"page"`
	PageSize int                      `json:"page_size"`
}

type PaymentAnomalyDetailResponse struct {
	PaymentAnomalyListItem
	Transitions []PaymentAnomalyTransitionItem `json:"transitions"`
}

// PaymentAnomalyTransitionRequest 仅记录人工处置状态，不会调用支付渠道或移动资金。
// expected_status 让后台操作以条件更新拒绝基于旧页面作出的决定。
type PaymentAnomalyTransitionRequest struct {
	ExpectedStatus          string `json:"expected_status" form:"expected_status" binding:"required"`
	TargetStatus            string `json:"target_status" form:"target_status" binding:"required"`
	Note                    string `json:"note" form:"note" binding:"required"`
	ExternalRefundReference string `json:"external_refund_reference" form:"external_refund_reference"`
}

type PaymentAnomalyTransitionResponse struct {
	PaymentAnomalyDetailResponse
	FundsTransferred bool `json:"funds_transferred"`
}
