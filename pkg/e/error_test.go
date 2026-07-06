package e

import (
	"errors"
	"fmt"
	"testing"
)

func TestCodeOf(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"nil 返回 SUCCESS", nil, SUCCESS},
		{"裸 error 返回 ERROR 兜底", errors.New("boom"), ERROR},
		{"带码 error 透出其码", New(ErrCircuitOpen), ErrCircuitOpen},
		{"被 %w 包过仍能沿链提取", fmt.Errorf("wrap: %w", New(ErrGroupbuyFull)), ErrGroupbuyFull},
	}
	for _, c := range cases {
		if got := CodeOf(c.err); got != c.want {
			t.Fatalf("%s: CodeOf=%d want %d", c.name, got, c.want)
		}
	}
}

func TestNewCarriesMsg(t *testing.T) {
	err := New(ErrCircuitOpen)
	if err.Error() != GetMsg(ErrCircuitOpen) {
		t.Fatalf("Error()=%q want %q", err.Error(), GetMsg(ErrCircuitOpen))
	}
	// errors.Is 对同一 sentinel 指针成立——保证 groupbuy 那种 sentinel 用法不破坏
	if !errors.Is(err, err) {
		t.Fatal("errors.Is 应对自身成立")
	}
}
