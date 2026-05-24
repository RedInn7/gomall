package initialize

import (
	"testing"

	"github.com/robfig/cron/v3"
)

// Cron 表达式合法性校验。
//
// 背景：本仓库 initialize/cron.go 历史上踩过 `* */5 * * * *` 实际触发 60×/5min
// 的坑（README "技术难点" #9 / deck 09 复盘），新加 job 的表达式必须经 robfig parser
// 校验通过，避免再次出现"看起来合理但触发 60×"。
//
// 这里不真起 cron.Start —— 那会需要 MySQL / Redis 基建。只校验：
//   1) 新加的 @every 5m / @every 1h 在 cron.WithSeconds() parser 下 parse 通过；
//   2) 现存表达式（含已知遗留的 60× 那个）也都 parse 通过，避免回归引入新 parse 错。

func TestCron_NewExpressions_ParseOK(t *testing.T) {
	p := cron.NewParser(
		cron.SecondOptional | cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor,
	)
	// 与 InitCron 内 c.AddFunc 传的字符串保持一致；任何一条改动 cron.go 都要同步这里。
	exprs := []string{
		"* */5 * * * *",   // OrderTimeoutCheck（已知遗留 60×/5min bug，仅校验 parse 通过）
		"0 */5 * * * *",   // RedPacketExpire 每 5min 整
		"0 0 */6 * * *",   // AutoConfirmReceive 每 6h
		"@every 5m",       // GroupbuyExpire 新加
		"@every 1h",       // PreorderForfeit 新加
	}
	for _, expr := range exprs {
		if _, err := p.Parse(expr); err != nil {
			t.Fatalf("cron 表达式 parse 失败 expr=%q err=%v", expr, err)
		}
	}
}

// 用 cron.New(cron.WithSeconds()) 真注册新表达式（不调 Start），
// 确认 AddFunc 不返回错误，等价于 InitCron 路径里那两个 if e != nil 分支不会被命中。
func TestCron_NewJobs_RegisterOK(t *testing.T) {
	c := cron.New(cron.WithSeconds())
	if _, err := c.AddFunc("@every 5m", func() {}); err != nil {
		t.Fatalf("GroupbuyExpire @every 5m 注册失败: %v", err)
	}
	if _, err := c.AddFunc("@every 1h", func() {}); err != nil {
		t.Fatalf("PreorderForfeit @every 1h 注册失败: %v", err)
	}
}
