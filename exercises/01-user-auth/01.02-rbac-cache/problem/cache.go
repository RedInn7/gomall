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
	// TODO: 实现角色查询与缓存。
	// 1. 如果 userID 的缓存存在，并且 now 严格早于 expiresAt，直接返回缓存的角色；
	// 2. 缓存不存在或已经到期时，调用 c.load(userID) 查询最新角色；
	// 3. c.load 返回错误时原样返回错误，不要写入或覆盖缓存；
	// 4. 查询成功后，把角色和 now.Add(c.ttl) 写入缓存，再返回角色。
	return "", nil
}

func (c *RoleCache) Invalidate(userID uint) {
	// TODO: 删除 userID 对应的缓存，让下一次 Lookup 必须调用 c.load 回源查询。
}
