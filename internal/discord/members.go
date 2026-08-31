// internal/discord/members.go
package discord

import (
	"strings"
	"sync"
)

// MemberCache tracks (snowflake -> username) for the tracking guild,
// rebuilt from Gateway events (see gateway.go) — nothing here is
// persisted, a reconnect gets a fresh snapshot that repopulates it.
type MemberCache struct {
	mu       sync.RWMutex
	username map[string]string
}

func NewMemberCache() *MemberCache {
	return &MemberCache{username: map[string]string{}}
}

func (c *MemberCache) Set(id, username string) {
	c.mu.Lock()
	c.username[id] = username
	c.mu.Unlock()
}

func (c *MemberCache) Remove(id string) {
	c.mu.Lock()
	delete(c.username, id)
	c.mu.Unlock()
}

func (c *MemberCache) Username(id string) (string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	u, ok := c.username[id]
	return u, ok
}

// FindByUsername scans for a member with this exact (case-insensitive)
// username. Discord usernames are globally unique, so at most one match is
// expected.
func (c *MemberCache) FindByUsername(username string) (string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	for id, u := range c.username {
		if strings.EqualFold(u, username) {
			return id, true
		}
	}
	return "", false
}
