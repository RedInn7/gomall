package clearing

import (
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func findPaymentAnomalies(db *gorm.DB, filter PaymentAnomalyListFilter) ([]PaymentAnomaly, int64, error) {
	query := applyPaymentAnomalyFilter(db.Model(&PaymentAnomaly{}), filter)
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var rows []PaymentAnomaly
	if err := applyPaymentAnomalyFilter(db.Model(&PaymentAnomaly{}), filter).
		Order("occurred_at DESC, id DESC").
		Offset((filter.Page - 1) * filter.PageSize).
		Limit(filter.PageSize).
		Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}

func findPaymentAnomalyDetail(db *gorm.DB, anomalyID uint) (*PaymentAnomaly, []PaymentAnomalyTransition, error) {
	var anomaly PaymentAnomaly
	if err := db.First(&anomaly, anomalyID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, ErrPaymentAnomalyNotFound
		}
		return nil, nil, err
	}
	var history []PaymentAnomalyTransition
	if err := db.Where("anomaly_id = ?", anomalyID).Order("acted_at ASC, id ASC").Find(&history).Error; err != nil {
		return nil, nil, err
	}
	return &anomaly, history, nil
}

func persistPaymentAnomalyTransition(
	db *gorm.DB,
	anomalyID uint,
	operatorID uint,
	req PaymentAnomalyTransitionRequest,
	now time.Time,
) error {
	return db.Transaction(func(tx *gorm.DB) error {
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
}

func applyPaymentAnomalyFilter(query *gorm.DB, filter PaymentAnomalyListFilter) *gorm.DB {
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
