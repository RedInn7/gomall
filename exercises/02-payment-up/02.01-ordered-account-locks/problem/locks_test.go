//go:build exercise

package orderedlocks

import (
	"reflect"
	"testing"
)

func TestOrderedAccountIDsIgnoresPaymentDirection(t *testing.T) {
	if got, want := OrderedAccountIDs(9, 3), []uint{3, 9}; !reflect.DeepEqual(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
	if got, want := OrderedAccountIDs(3, 9), []uint{3, 9}; !reflect.DeepEqual(got, want) {
		t.Fatalf("reverse order = %v, want %v", got, want)
	}
}

func TestOrderedAccountIDsLocksSameAccountOnce(t *testing.T) {
	if got, want := OrderedAccountIDs(7, 7), []uint{7}; !reflect.DeepEqual(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
}
