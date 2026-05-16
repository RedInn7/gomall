package service

import (
	"context"
	"encoding/json"
	"io"
	"testing"

	"github.com/sirupsen/logrus"

	logpkg "github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/service/events"
)

func initLogForTest() {
	if logpkg.LogrusObj == nil {
		l := logrus.New()
		l.Out = io.Discard
		logpkg.LogrusObj = l
	}
}

func TestRedPacket_ConsumerHandlesClaimedEvent(t *testing.T) {
	initLogForTest()

	payload, _ := json.Marshal(events.RedPacketClaimed{
		RedPacketID: 1,
		UserID:      42,
		Amount:      123,
	})
	if err := HandleRedPacketClaimedEvent(context.Background(), payload); err != nil {
		t.Fatalf("handle claimed: %v", err)
	}
}

func TestRedPacket_ConsumerRejectsBadPayload(t *testing.T) {
	initLogForTest()
	if err := HandleRedPacketClaimedEvent(context.Background(), []byte("not-json")); err == nil {
		t.Fatal("expected decode error")
	}
	if err := HandleRedPacketExpiredEvent(context.Background(), []byte("nope")); err == nil {
		t.Fatal("expected decode error")
	}
}

func TestRedPacket_ConsumerHandlesExpiredEvent(t *testing.T) {
	initLogForTest()
	payload, _ := json.Marshal(events.RedPacketExpired{
		RedPacketID: 7,
		UserID:      9,
		RefundTotal: 500,
	})
	if err := HandleRedPacketExpiredEvent(context.Background(), payload); err != nil {
		t.Fatalf("handle expired: %v", err)
	}
}
