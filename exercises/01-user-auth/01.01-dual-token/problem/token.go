//go:build exercise

package dualtoken

import "time"

type Action string

const (
	Pass    Action = "PASS"
	Refresh Action = "REFRESH"
	Relogin Action = "RELOGIN"
)

func NextAction(now, accessExpiresAt, refreshExpiresAt time.Time) Action {
	// TODO: 根据两个令牌的过期时间决定下一步：
	// 1. now 严格早于 accessExpiresAt 时返回 Pass；
	// 2. access token 已过期，但 now 严格早于 refreshExpiresAt 时返回 Refresh；
	// 3. 两个令牌都已过期时返回 Relogin。
	// now 等于过期时间也视为已经过期。
	return ""
}
