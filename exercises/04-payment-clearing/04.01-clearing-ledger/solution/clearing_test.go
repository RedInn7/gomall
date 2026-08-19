//go:build exercise

package clearingledger

import (
	"errors"
	"reflect"
	"testing"
)

func validOrder() *Order {
	return &Order{ID: 101, BuyerID: 11, SellerID: 22, Num: 2, Money: 500}
}

func TestRecordClearedTxWallet(t *testing.T) {
	store := NewStore()
	after := int64(8_000)
	if err := RecordClearedTx(store, validOrder(), ChannelWallet, "", "cny", &after); err != nil {
		t.Fatalf("RecordClearedTx() error = %v", err)
	}

	record, ok := store.Clearing(101)
	if !ok || record.GrossCents != 1_000 || record.Status != StatusCleared {
		t.Fatalf("clearing = %+v, ok = %v", record, ok)
	}
	want := []LedgerEntry{
		{OrderID: 101, UserID: 11, AccountCode: AccountUserWallet, Direction: DirectionDebit, AmountCents: 1_000, BalanceAfterCents: 8_000, BizType: BizTypeOrderClear},
		{OrderID: 101, AccountCode: AccountMerchantEscrow, Direction: DirectionCredit, AmountCents: 1_000, BizType: BizTypeOrderClear},
	}
	if got := store.Entries(); !reflect.DeepEqual(got, want) {
		t.Fatalf("entries = %+v, want %+v", got, want)
	}
}

func TestRecordClearedTxStripe(t *testing.T) {
	store := NewStore()
	if err := RecordClearedTx(store, validOrder(), ChannelStripe, "pi_123", "usd", nil); err != nil {
		t.Fatalf("RecordClearedTx() error = %v", err)
	}
	assertExternalPair(t, store, "pi_123", "USD")
}

func TestRecordClearedTxWeb3(t *testing.T) {
	store := NewStore()
	if err := RecordClearedTx(store, validOrder(), ChannelWeb3, "0xabc", "eth", nil); err != nil {
		t.Fatalf("RecordClearedTx() error = %v", err)
	}
	assertExternalPair(t, store, "0xabc", "ETH")
}

func TestRecordClearedTxRequiresWalletBalance(t *testing.T) {
	store := NewStore()
	err := RecordClearedTx(store, validOrder(), ChannelWallet, "", "CNY", nil)
	assertRejectedWithoutWrites(t, store, err, ErrInvalidClearingInput)
}

func TestRecordClearedTxRejectsBalanceForExternalChannels(t *testing.T) {
	for _, channel := range []string{ChannelStripe, ChannelWeb3} {
		t.Run(channel, func(t *testing.T) {
			store := NewStore()
			after := int64(100)
			err := RecordClearedTx(store, validOrder(), channel, "ref", "USD", &after)
			assertRejectedWithoutWrites(t, store, err, ErrInvalidClearingInput)
		})
	}
}

func TestRecordClearedTxRejectsUnknownChannel(t *testing.T) {
	store := NewStore()
	err := RecordClearedTx(store, validOrder(), "cash", "", "CNY", nil)
	assertRejectedWithoutWrites(t, store, err, ErrInvalidClearingInput)
}

func TestRecordClearedTxRejectsBlankCurrency(t *testing.T) {
	store := NewStore()
	err := RecordClearedTx(store, validOrder(), ChannelStripe, "pi_123", "  ", nil)
	assertRejectedWithoutWrites(t, store, err, ErrInvalidClearingInput)
}

func TestRecordClearedTxNormalizesProviderRefAndCurrency(t *testing.T) {
	store := NewStore()
	if err := RecordClearedTx(store, validOrder(), ChannelStripe, "  pi_123  ", " usd ", nil); err != nil {
		t.Fatalf("RecordClearedTx() error = %v", err)
	}
	record, _ := store.Clearing(101)
	if record.ProviderRef != "pi_123" || record.Currency != "USD" {
		t.Fatalf("record = %+v", record)
	}
}

func TestRecordClearedTxUsesMoneyTimesQuantity(t *testing.T) {
	store := NewStore()
	o := validOrder()
	o.Num = 3
	if err := RecordClearedTx(store, o, ChannelStripe, "pi", "USD", nil); err != nil {
		t.Fatalf("RecordClearedTx() error = %v", err)
	}
	assertAmounts(t, store, 1_500)
}

func TestRecordClearedTxUsesPromotionalFinalCents(t *testing.T) {
	store := NewStore()
	o := validOrder()
	o.PromoRuleID = 7
	o.FinalCents = 799
	if err := RecordClearedTx(store, o, ChannelStripe, "pi", "USD", nil); err != nil {
		t.Fatalf("RecordClearedTx() error = %v", err)
	}
	assertAmounts(t, store, 799)
}

func TestRecordClearedTxRejectsInvalidOrder(t *testing.T) {
	tests := []struct {
		name  string
		order *Order
	}{
		{name: "nil order", order: nil},
		{name: "zero order id", order: &Order{BuyerID: 11, SellerID: 22, Num: 1}},
		{name: "zero buyer id", order: &Order{ID: 1, SellerID: 22, Num: 1}},
		{name: "zero seller id", order: &Order{ID: 1, BuyerID: 11, Num: 1}},
		{name: "zero quantity", order: &Order{ID: 1, BuyerID: 11, SellerID: 22}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewStore()
			err := RecordClearedTx(store, tt.order, ChannelStripe, "pi", "USD", nil)
			assertRejectedWithoutWrites(t, store, err, ErrInvalidClearingInput)
		})
	}
}

func TestRecordClearedTxRejectsNilStore(t *testing.T) {
	err := RecordClearedTx(nil, validOrder(), ChannelStripe, "pi", "USD", nil)
	if !errors.Is(err, ErrInvalidClearingInput) {
		t.Fatalf("error = %v, want %v", err, ErrInvalidClearingInput)
	}
}

func TestRecordClearedTxRejectsNegativeGross(t *testing.T) {
	store := NewStore()
	o := validOrder()
	o.Money = -1
	err := RecordClearedTx(store, o, ChannelStripe, "pi", "USD", nil)
	assertRejectedWithoutWrites(t, store, err, ErrInvalidClearingInput)
}

func TestRecordClearedTxAllowsZeroGross(t *testing.T) {
	store := NewStore()
	o := validOrder()
	o.Money = 0
	if err := RecordClearedTx(store, o, ChannelStripe, "pi", "USD", nil); err != nil {
		t.Fatalf("RecordClearedTx() error = %v", err)
	}
	assertAmounts(t, store, 0)
}

func TestRecordClearedTxRejectsDuplicateOrder(t *testing.T) {
	store := NewStore()
	o := validOrder()
	if err := RecordClearedTx(store, o, ChannelStripe, "pi_1", "USD", nil); err != nil {
		t.Fatalf("first RecordClearedTx() error = %v", err)
	}
	before := store.Entries()
	err := RecordClearedTx(store, o, ChannelWeb3, "0xabc", "ETH", nil)
	if !errors.Is(err, ErrDuplicateOrder) {
		t.Fatalf("second error = %v, want %v", err, ErrDuplicateOrder)
	}
	if got := store.Entries(); !reflect.DeepEqual(got, before) {
		t.Fatalf("duplicate changed entries: before=%+v after=%+v", before, got)
	}
}

func TestRecordClearedTxRollsBackWhenEscrowCreditFails(t *testing.T) {
	store := NewStore()
	store.FailEscrowCredit(true)
	err := RecordClearedTx(store, validOrder(), ChannelStripe, "pi", "USD", nil)
	assertRejectedWithoutWrites(t, store, err, ErrEscrowCreditFailed)
}

func TestEntriesReturnsSnapshot(t *testing.T) {
	store := NewStore()
	if err := RecordClearedTx(store, validOrder(), ChannelStripe, "pi", "USD", nil); err != nil {
		t.Fatalf("RecordClearedTx() error = %v", err)
	}
	entries := store.Entries()
	if len(entries) != 2 {
		t.Fatalf("len(entries) = %d, want 2", len(entries))
	}
	entries[0].AmountCents = 99
	if store.Entries()[0].AmountCents != 1_000 {
		t.Fatal("Entries() exposed mutable store state")
	}
}

func assertExternalPair(t *testing.T, store *Store, providerRef, currency string) {
	t.Helper()
	record, ok := store.Clearing(101)
	if !ok || record.ProviderRef != providerRef || record.Currency != currency {
		t.Fatalf("clearing = %+v, ok = %v", record, ok)
	}
	entries := store.Entries()
	if len(entries) != 2 {
		t.Fatalf("len(entries) = %d, want 2", len(entries))
	}
	if entries[0].UserID != 0 || entries[0].AccountCode != AccountExternalClearing || entries[0].Direction != DirectionDebit {
		t.Fatalf("debit = %+v", entries[0])
	}
	if entries[1].UserID != 0 || entries[1].AccountCode != AccountMerchantEscrow || entries[1].Direction != DirectionCredit {
		t.Fatalf("credit = %+v", entries[1])
	}
}

func assertAmounts(t *testing.T, store *Store, want int64) {
	t.Helper()
	record, ok := store.Clearing(101)
	if !ok || record.GrossCents != want {
		t.Fatalf("clearing = %+v, ok = %v, want amount %d", record, ok, want)
	}
	entries := store.Entries()
	if len(entries) != 2 || entries[0].AmountCents != want || entries[1].AmountCents != want {
		t.Fatalf("entries = %+v, want both amounts %d", entries, want)
	}
}

func assertRejectedWithoutWrites(t *testing.T, store *Store, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
	if len(store.Entries()) != 0 {
		t.Fatalf("entries = %+v, want none", store.Entries())
	}
	if _, ok := store.Clearing(101); ok {
		t.Fatal("unexpected clearing record")
	}
}
