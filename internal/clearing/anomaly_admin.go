package clearing

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/RedInn7/gomall/pkg/utils/ctl"
	"github.com/RedInn7/gomall/repository/db/dao"
)

var (
	ErrPaymentAnomalyNotFound       = errors.New("异常款记录不存在")
	ErrInvalidAnomalyFilter         = errors.New("异常款查询条件不合法")
	ErrInvalidAnomalyTransition     = errors.New("异常款状态转换不合法")
	ErrPaymentAnomalyStatusConflict = errors.New("异常款状态已变化，请刷新后重试")
	ErrAnomalyNoteRequired          = errors.New("状态转换必须填写备注")
	ErrExternalRefundRefRequired    = errors.New("标记为已退款时必须填写外部退款凭证")
	ErrUnexpectedExternalRefundRef  = errors.New("只有标记为已退款时才能填写外部退款凭证")
)

const (
	defaultAnomalyPageSize = 50
	maxAnomalyPageSize     = 200
	maxAnomalyNoteLength   = 1024
)

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

func ListPaymentAnomalies(ctx context.Context, filter PaymentAnomalyListFilter) (*PaymentAnomalyListResponse, error) {
	return listPaymentAnomalies(dao.NewDBClient(ctx), filter)
}

func listPaymentAnomalies(db *gorm.DB, filter PaymentAnomalyListFilter) (*PaymentAnomalyListResponse, error) {
	if db == nil {
		return nil, ErrInvalidAnomalyFilter
	}
	filter = normalizeAnomalyFilter(filter)
	if !validOptionalAnomalyStatus(filter.Status) || !validOptionalAnomalyChannel(filter.Channel) ||
		!validOptionalAnomalyReason(filter.Reason) {
		return nil, ErrInvalidAnomalyFilter
	}

	query := applyAnomalyFilter(db.Model(&PaymentAnomaly{}), filter)
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, err
	}

	var rows []PaymentAnomaly
	if err := applyAnomalyFilter(db.Model(&PaymentAnomaly{}), filter).
		Order("occurred_at DESC, id DESC").
		Offset((filter.Page - 1) * filter.PageSize).
		Limit(filter.PageSize).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	items := make([]PaymentAnomalyListItem, 0, len(rows))
	for i := range rows {
		items = append(items, anomalyListItem(&rows[i]))
	}
	return &PaymentAnomalyListResponse{Items: items, Total: total, Page: filter.Page, PageSize: filter.PageSize}, nil
}

func GetPaymentAnomaly(ctx context.Context, anomalyID uint) (*PaymentAnomalyDetailResponse, error) {
	return getPaymentAnomaly(dao.NewDBClient(ctx), anomalyID)
}

func getPaymentAnomaly(db *gorm.DB, anomalyID uint) (*PaymentAnomalyDetailResponse, error) {
	if db == nil || anomalyID == 0 {
		return nil, ErrPaymentAnomalyNotFound
	}
	var anomaly PaymentAnomaly
	if err := db.First(&anomaly, anomalyID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrPaymentAnomalyNotFound
		}
		return nil, err
	}
	var history []PaymentAnomalyTransition
	if err := db.Where("anomaly_id = ?", anomalyID).Order("acted_at ASC, id ASC").Find(&history).Error; err != nil {
		return nil, err
	}
	return anomalyDetail(&anomaly, history), nil
}

// RecordPaymentAnomalyTransition 持久化管理员的人工处置结论。
// 即使 target_status=refunded，本函数也只记录外部退款已经完成的事实，绝不自动转账。
func RecordPaymentAnomalyTransition(ctx context.Context, anomalyID uint, req PaymentAnomalyTransitionRequest) (*PaymentAnomalyTransitionResponse, error) {
	operator, err := ctl.GetUserInfo(ctx)
	if err != nil || operator.Id == 0 {
		return nil, ErrInvalidAnomalyTransition
	}
	return recordPaymentAnomalyTransition(dao.NewDBClient(ctx), anomalyID, operator.Id, req)
}

func recordPaymentAnomalyTransition(db *gorm.DB, anomalyID, operatorID uint, req PaymentAnomalyTransitionRequest) (*PaymentAnomalyTransitionResponse, error) {
	if db == nil || anomalyID == 0 || operatorID == 0 {
		return nil, ErrInvalidAnomalyTransition
	}
	req.ExpectedStatus = strings.TrimSpace(req.ExpectedStatus)
	req.TargetStatus = strings.TrimSpace(req.TargetStatus)
	req.Note = strings.TrimSpace(req.Note)
	req.ExternalRefundReference = strings.TrimSpace(req.ExternalRefundReference)
	if req.Note == "" || utf8.RuneCountInString(req.Note) > maxAnomalyNoteLength {
		return nil, ErrAnomalyNoteRequired
	}
	if !validAnomalyStatus(req.ExpectedStatus) || !validAnomalyStatus(req.TargetStatus) ||
		!allowedAnomalyTransition(req.ExpectedStatus, req.TargetStatus) {
		return nil, ErrInvalidAnomalyTransition
	}
	if req.TargetStatus == AnomalyStatusRefunded {
		if req.ExternalRefundReference == "" {
			return nil, ErrExternalRefundRefRequired
		}
	} else if req.ExternalRefundReference != "" {
		return nil, ErrUnexpectedExternalRefundRef
	}

	now := time.Now()
	err := db.Transaction(func(tx *gorm.DB) error {
		var anomaly PaymentAnomaly
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&anomaly, anomalyID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrPaymentAnomalyNotFound
			}
			return err
		}
		if anomaly.Status != req.ExpectedStatus {
			return ErrPaymentAnomalyStatusConflict
		}

		updates := map[string]interface{}{
			"status":              req.TargetStatus,
			"last_operator_id":    operatorID,
			"last_action_at":      now,
			"last_note":           req.Note,
			"external_refund_ref": req.ExternalRefundReference,
		}
		if isTerminalAnomalyStatus(req.TargetStatus) {
			updates["resolved_at"] = now
		} else {
			updates["resolved_at"] = nil
		}
		result := tx.Model(&PaymentAnomaly{}).
			Where("id = ? AND status = ?", anomalyID, req.ExpectedStatus).
			Updates(updates)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrPaymentAnomalyStatusConflict
		}

		return tx.Create(&PaymentAnomalyTransition{
			AnomalyID:         anomalyID,
			FromStatus:        req.ExpectedStatus,
			ToStatus:          req.TargetStatus,
			OperatorID:        operatorID,
			Note:              req.Note,
			ExternalRefundRef: req.ExternalRefundReference,
			ActedAt:           now,
		}).Error
	})
	if err != nil {
		return nil, err
	}
	detail, err := getPaymentAnomaly(db, anomalyID)
	if err != nil {
		return nil, err
	}
	return &PaymentAnomalyTransitionResponse{PaymentAnomalyDetailResponse: *detail, FundsTransferred: false}, nil
}

func normalizeAnomalyFilter(filter PaymentAnomalyListFilter) PaymentAnomalyListFilter {
	if filter.Page <= 0 {
		filter.Page = 1
	}
	if filter.PageSize <= 0 || filter.PageSize > maxAnomalyPageSize {
		filter.PageSize = defaultAnomalyPageSize
	}
	filter.Status = strings.TrimSpace(filter.Status)
	filter.Channel = strings.TrimSpace(filter.Channel)
	filter.Reason = strings.TrimSpace(filter.Reason)
	return filter
}

func applyAnomalyFilter(query *gorm.DB, filter PaymentAnomalyListFilter) *gorm.DB {
	if filter.Status != "" {
		query = query.Where("status = ?", filter.Status)
	}
	if filter.Channel != "" {
		query = query.Where("channel = ?", filter.Channel)
	}
	if filter.Reason != "" {
		query = query.Where("reason = ?", filter.Reason)
	}
	if filter.OrderID != 0 {
		query = query.Where("order_id = ?", filter.OrderID)
	}
	return query
}

func validAnomalyStatus(status string) bool {
	switch status {
	case AnomalyStatusPendingReview, AnomalyStatusReviewing, AnomalyStatusResolved, AnomalyStatusRefunded, AnomalyStatusRejected:
		return true
	default:
		return false
	}
}

func validOptionalAnomalyStatus(status string) bool {
	return status == "" || validAnomalyStatus(status)
}

func validOptionalAnomalyChannel(channel string) bool {
	return channel == "" || isExternalChannel(channel)
}

func validOptionalAnomalyReason(reason string) bool {
	if reason == "" {
		return true
	}
	switch reason {
	case AnomalyReasonDuplicatePayment, AnomalyReasonAmountMismatch, AnomalyReasonPaymentDetailsMismatch, AnomalyReasonHighRiskPayment:
		return true
	default:
		return false
	}
}

func allowedAnomalyTransition(from, to string) bool {
	if from == AnomalyStatusPendingReview {
		return to == AnomalyStatusReviewing
	}
	if from == AnomalyStatusReviewing {
		return isTerminalAnomalyStatus(to)
	}
	return false
}

func isTerminalAnomalyStatus(status string) bool {
	return status == AnomalyStatusResolved || status == AnomalyStatusRefunded || status == AnomalyStatusRejected
}

func anomalyListItem(anomaly *PaymentAnomaly) PaymentAnomalyListItem {
	return PaymentAnomalyListItem{
		ID: anomaly.ID, OrderID: anomaly.OrderID, Channel: anomaly.Channel,
		ProviderRef: anomaly.ProviderRef, ProviderAmount: anomaly.ProviderAmount,
		Currency: anomaly.Currency, Reason: anomaly.Reason, Status: anomaly.Status,
		OccurredAt: anomaly.OccurredAt, LastOperatorID: anomaly.LastOperatorID,
		LastActionAt: anomaly.LastActionAt, LastNote: anomaly.LastNote,
		ExternalRefundRef: anomaly.ExternalRefundRef, ResolvedAt: anomaly.ResolvedAt,
	}
}

func anomalyDetail(anomaly *PaymentAnomaly, history []PaymentAnomalyTransition) *PaymentAnomalyDetailResponse {
	transitions := make([]PaymentAnomalyTransitionItem, 0, len(history))
	for _, entry := range history {
		transitions = append(transitions, PaymentAnomalyTransitionItem{
			ID: entry.ID, FromStatus: entry.FromStatus, ToStatus: entry.ToStatus,
			OperatorID: entry.OperatorID, Note: entry.Note,
			ExternalRefundRef: entry.ExternalRefundRef, ActedAt: entry.ActedAt,
		})
	}
	return &PaymentAnomalyDetailResponse{PaymentAnomalyListItem: anomalyListItem(anomaly), Transitions: transitions}
}
