package feishu

import (
	"context"
	"sync"
	"time"
)

type cacheEntry struct {
	value   string
	expires time.Time
}
type tokenCache struct {
	mu     sync.Mutex
	values map[string]cacheEntry
	now    func() time.Time
}

func (c *tokenCache) Get(ctx context.Context, key string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	entry := c.values[key]
	if !c.now().Before(entry.expires) {
		delete(c.values, key)
		return "", nil
	}
	return entry.value, nil
}
func (c *tokenCache) Set(ctx context.Context, key, value string, ttl time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.values[key] = cacheEntry{value: value, expires: c.now().Add(ttl)}
	return nil
}
