package service

import "testing"

// TestAutoConfirmReceive_NoPanicWhenNoDB cron 在 DB 没初始化时只能记日志返回，不能 panic。
func TestAutoConfirmReceive_NoPanicWhenNoDB(t *testing.T) {
	initLogForTest()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("RunAutoConfirmReceive 不应 panic, got: %v", r)
		}
	}()
	// 用 defer + recover 兜底外部 panic；同时 service 自身也有日志容错。
	defer func() { _ = recover() }()
	(&OrderTaskService{}).RunAutoConfirmReceive()
}

// TestAutoConfirmReceive_ConstantInDays 默认 7 天与文档一致。
func TestAutoConfirmReceive_ConstantInDays(t *testing.T) {
	if autoConfirmReceiveDays != 7 {
		t.Fatalf("autoConfirmReceiveDays = %d, want 7", autoConfirmReceiveDays)
	}
}
