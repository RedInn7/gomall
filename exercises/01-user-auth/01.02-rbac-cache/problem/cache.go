//go:build exercise

package rbaccache

import "time"

type Loader func(userID uint) (string, error)

type entry struct {
	role      string
	expiresAt time.Time
}

type RoleCache struct {
	ttl    time.Duration
	load   Loader
	values map[uint]entry
}

func NewRoleCache(ttl time.Duration, load Loader) *RoleCache {
	return &RoleCache{ttl: ttl, load: load, values: make(map[uint]entry)}
}

func (c *RoleCache) Lookup(userID uint, now time.Time) (string, error) {
	// TODO
	return "", nil
}

func (c *RoleCache) Invalidate(userID uint) {
	// TODO
}
