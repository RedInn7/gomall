package clearing

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/RedInn7/gomall/pkg/utils/ctl"
)

func newAnomalyAdminTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "anomaly.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&PaymentAnomaly{}, &PaymentAnomalyTransition{}); err != nil {
		t.Fatalf("migrate anomaly tables: %v", err)
	}
	return db
}

func createTestAnomaly(t *testing.T, db *gorm.DB, orderID uint, channel, status string, occurredAt time.Time) PaymentAnomaly {
	t.Helper()
	anomaly := PaymentAnomaly{
		OrderID: orderID, Channel: channel, ProviderRef: channel + "-ref-" + time.Now().String(),
		ProviderAmount: "100.00", Currency: "USD", Reason: AnomalyReasonDuplicatePayment,
		Status: status, OccurredAt: occurredAt,
	}
	if err := db.Create(&anomaly).Error; err != nil {
		t.Fatalf("create anomaly: %v", err)
	}
	return anomaly
}

func TestListPaymentAnomaliesPaginatesAndFilters(t *testing.T) {
	db := newAnomalyAdminTestDB(t)
	base := time.Now().Add(-time.Hour)
	first := createTestAnomaly(t, db, 101, ChannelStripe, AnomalyStatusPendingReview, base)
	second := createTestAnomaly(t, db, 102, ChannelStripe, AnomalyStatusPendingReview, base.Add(time.Minute))
	createTestAnomaly(t, db, 103, ChannelWeb3, AnomalyStatusReviewing, base.Add(2*time.Minute))

	page, err := listPaymentAnomalies(db, PaymentAnomalyListFilter{
		Page: 1, PageSize: 1, Status: AnomalyStatusPendingReview, Channel: ChannelStripe,
	})
	if err != nil {
		t.Fatalf("list anomalies: %v", err)
	}
	if page.Total != 2 || len(page.Items) != 1 || page.Items[0].ID != second.ID {
		t.Fatalf("unexpected first page: %+v", page)
	}

	page, err = listPaymentAnomalies(db, PaymentAnomalyListFilter{
		Page: 2, PageSize: 1, Status: AnomalyStatusPendingReview, Channel: ChannelStripe,
	})
	if err != nil {
		t.Fatalf("list second page: %v", err)
	}
	if page.Total != 2 || len(page.Items) != 1 || page.Items[0].ID != first.ID {
		t.Fatalf("unexpected second page: %+v", page)
	}

	if _, err := listPaymentAnomalies(db, PaymentAnomalyListFilter{Status: "made_up"}); !errors.Is(err, ErrInvalidAnomalyFilter) {
		t.Fatalf("invalid status error = %v", err)
	}
}

func TestRecordPaymentAnomalyTransitionCreatesAuditAndNeverMovesFunds(t *testing.T) {
	db := newAnomalyAdminTestDB(t)
	anomaly := createTestAnomaly(t, db, 201, ChannelXcash, AnomalyStatusPendingReview, time.Now())

	result, err := recordPaymentAnomalyTransition(db, anomaly.ID, 77, PaymentAnomalyTransitionRequest{
		ExpectedStatus: AnomalyStatusPendingReview,
		TargetStatus:   AnomalyStatusReviewing,
		Note:           "已核对渠道账单，开始人工复核",
	})
	if err != nil {
		t.Fatalf("start review: %v", err)
	}
	if result.FundsTransferred {
		t.Fatal("status recording must not claim that funds were transferred")
	}
	if result.Status != AnomalyStatusReviewing || result.LastOperatorID != 77 || result.ResolvedAt != nil {
		t.Fatalf("unexpected reviewing result: %+v", result)
	}
	if len(result.Transitions) != 1 || result.Transitions[0].OperatorID != 77 ||
		result.Transitions[0].FromStatus != AnomalyStatusPendingReview || result.Transitions[0].ToStatus != AnomalyStatusReviewing {
		t.Fatalf("unexpected history: %+v", result.Transitions)
	}

	if _, err := recordPaymentAnomalyTransition(db, anomaly.ID, 78, PaymentAnomalyTransitionRequest{
		ExpectedStatus: AnomalyStatusPendingReview, TargetStatus: AnomalyStatusReviewing, Note: "旧页面重复操作",
	}); !errors.Is(err, ErrPaymentAnomalyStatusConflict) {
		t.Fatalf("stale transition error = %v", err)
	}

	if _, err := recordPaymentAnomalyTransition(db, anomaly.ID, 77, PaymentAnomalyTransitionRequest{
		ExpectedStatus: AnomalyStatusReviewing, TargetStatus: AnomalyStatusRefunded, Note: "渠道退款完成",
	}); !errors.Is(err, ErrExternalRefundRefRequired) {
		t.Fatalf("missing external refund reference error = %v", err)
	}

	result, err = recordPaymentAnomalyTransition(db, anomaly.ID, 77, PaymentAnomalyTransitionRequest{
		ExpectedStatus: AnomalyStatusReviewing, TargetStatus: AnomalyStatusRefunded,
		Note: "已在 Xcash 后台完成原路退款", ExternalRefundReference: "xcash-refund-9001",
	})
	if err != nil {
		t.Fatalf("record external refund: %v", err)
	}
	if result.Status != AnomalyStatusRefunded || result.ResolvedAt == nil ||
		result.ExternalRefundRef != "xcash-refund-9001" || len(result.Transitions) != 2 {
		t.Fatalf("unexpected refunded result: %+v", result)
	}
}

func TestRecordPaymentAnomalyTransitionValidatesStateMatrix(t *testing.T) {
	db := newAnomalyAdminTestDB(t)
	pending := createTestAnomaly(t, db, 301, ChannelStripe, AnomalyStatusPendingReview, time.Now())
	if _, err := recordPaymentAnomalyTransition(db, pending.ID, 1, PaymentAnomalyTransitionRequest{
		ExpectedStatus: AnomalyStatusPendingReview, TargetStatus: AnomalyStatusResolved, Note: "跳过复核",
	}); !errors.Is(err, ErrInvalidAnomalyTransition) {
		t.Fatalf("direct terminal transition error = %v", err)
	}

	for _, target := range []string{AnomalyStatusResolved, AnomalyStatusRejected} {
		t.Run(target, func(t *testing.T) {
			row := createTestAnomaly(t, db, 400+uint(len(target)), ChannelWeb3, AnomalyStatusReviewing, time.Now())
			result, err := recordPaymentAnomalyTransition(db, row.ID, 9, PaymentAnomalyTransitionRequest{
				ExpectedStatus: AnomalyStatusReviewing, TargetStatus: target, Note: "人工核对完成",
			})
			if err != nil {
				t.Fatalf("record %s: %v", target, err)
			}
			if result.Status != target || result.ResolvedAt == nil {
				t.Fatalf("unexpected terminal result: %+v", result)
			}
		})
	}

	reviewing := createTestAnomaly(t, db, 501, ChannelStripe, AnomalyStatusReviewing, time.Now())
	if _, err := recordPaymentAnomalyTransition(db, reviewing.ID, 2, PaymentAnomalyTransitionRequest{
		ExpectedStatus: AnomalyStatusReviewing, TargetStatus: AnomalyStatusRejected,
		Note: "无需退款", ExternalRefundReference: "must-not-be-present",
	}); !errors.Is(err, ErrUnexpectedExternalRefundRef) {
		t.Fatalf("unexpected refund reference error = %v", err)
	}
}

func TestRecordPaymentAnomalyTransitionRollsBackWithoutAuditTable(t *testing.T) {
	db := newAnomalyAdminTestDB(t)
	anomaly := createTestAnomaly(t, db, 601, ChannelStripe, AnomalyStatusPendingReview, time.Now())
	if err := db.Migrator().DropTable(&PaymentAnomalyTransition{}); err != nil {
		t.Fatalf("drop audit table: %v", err)
	}

	_, err := recordPaymentAnomalyTransition(db, anomaly.ID, 3, PaymentAnomalyTransitionRequest{
		ExpectedStatus: AnomalyStatusPendingReview, TargetStatus: AnomalyStatusReviewing, Note: "开始复核",
	})
	if err == nil {
		t.Fatal("expected audit insert failure")
	}
	var got PaymentAnomaly
	if err := db.First(&got, anomaly.ID).Error; err != nil {
		t.Fatalf("reload anomaly: %v", err)
	}
	if got.Status != AnomalyStatusPendingReview || got.LastOperatorID != 0 || got.LastActionAt != nil {
		t.Fatalf("anomaly update was not rolled back: %+v", got)
	}
}

func TestRecordPaymentAnomalyTransitionUsesAuthenticatedOperator(t *testing.T) {
	// The exported service rejects unauthenticated calls before opening a database transaction.
	if _, err := RecordPaymentAnomalyTransition(context.Background(), 1, PaymentAnomalyTransitionRequest{}); !errors.Is(err, ErrInvalidAnomalyTransition) {
		t.Fatalf("unauthenticated error = %v", err)
	}
	ctx := ctl.NewContext(context.Background(), &ctl.UserInfo{Id: 42})
	if userInfo, err := ctl.GetUserInfo(ctx); err != nil || userInfo.Id != 42 {
		t.Fatalf("operator context = %+v, %v", userInfo, err)
	}
}
