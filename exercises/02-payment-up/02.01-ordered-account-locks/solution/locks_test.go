//go:build exercise

package orderedlocks

import (
	"reflect"
	"testing"
)

func TestOrderedAccountIDs(t *testing.T) {
	for _, tt := range []struct {
		a, b uint
		want []uint
	}{{9, 3, []uint{3, 9}}, {3, 9, []uint{3, 9}}, {7, 7, []uint{7}}} {
		if got := OrderedAccountIDs(tt.a, tt.b); !reflect.DeepEqual(got, tt.want) {
			t.Fatalf("OrderedAccountIDs(%d, %d) = %v, want %v", tt.a, tt.b, got, tt.want)
		}
	}
}
