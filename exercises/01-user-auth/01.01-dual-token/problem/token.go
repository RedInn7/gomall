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
	// TODO
	return ""
}
