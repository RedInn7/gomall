package redpacket

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/sirupsen/logrus"

	logpkg "github.com/RedInn7/gomall/pkg/utils/log"
)

func initLogForTest() {
	if logpkg.LogrusObj == nil {
		l := logrus.New()
		l.Out = io.Discard
		logpkg.LogrusObj = l
	}
}

// 毒消息（解码失败 / 关键字段缺失 / 未知 routing key）必须判为不可重试，直接进 DLQ。
// 这些分支在反序列化/字段校验阶段返回，不触达 DB，可脱离基础设施直接验证。
func TestRedPacket_DispatchPoisonMessages(t *testing.T) {
	initLogForTest()
	ctx := context.Background()

	cases := []struct {
		name       string
		routingKey string
		payload    []byte
	}{
		{"bad-created-json", "red_packet.created", []byte("not-json")},
		{"bad-claimed-json", "red_packet.claimed", []byte("nope")},
		{"bad-expired-json", "red_packet.expired", []byte("{")},
		{"created-zero-total", "red_packet.created", []byte(`{"red_packet_id":1,"user_id":2,"total":0}`)},
		{"claimed-zero-amount", "red_packet.claimed", []byte(`{"red_packet_id":1,"user_id":2,"amount":0}`)},
		{"expired-missing-user", "red_packet.expired", []byte(`{"red_packet_id":1,"refund_total":100}`)},
		{"unknown-routing-key", "red_packet.unknown", []byte(`{}`)},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := DispatchSettleEvent(ctx, c.routingKey, c.payload)
			if err == nil {
				t.Fatalf("expected poison error, got nil")
			}
			if !errors.Is(err, errSettlePoisonMessage) {
				t.Fatalf("expected poison error, got %v", err)
			}
		})
	}
}

// red_packet.expired 且 refund_total<=0 时无资金可退，应幂等放行（不进 DLQ、不报错）。
func TestRedPacket_ExpiredZeroRefundIsNoop(t *testing.T) {
	initLogForTest()
	if err := HandleRedPacketExpiredEvent(context.Background(),
		[]byte(`{"red_packet_id":1,"user_id":2,"refund_total":0}`)); err != nil {
		t.Fatalf("zero refund should be noop, got %v", err)
	}
}
