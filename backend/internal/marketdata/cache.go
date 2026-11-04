package marketdata

import (
	"container/list"
	"sync"
	"time"
)

type cacheEntry struct {
	key     string
	value   []byte
	expires time.Time
}

// Cache is a bounded, concurrent LRU usable by any external provider.
type Cache struct {
	mu       sync.Mutex
	entries  map[string]*list.Element
	order    *list.List
	capacity int
	ttl      time.Duration
	now      func() time.Time
}

func NewCache(capacity int, ttl time.Duration) *Cache {
	return &Cache{entries: make(map[string]*list.Element), order: list.New(), capacity: capacity, ttl: ttl, now: time.Now}
}
func (c *Cache) Get(key string) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	if !ok {
		return nil, false
	}
	v := e.Value.(cacheEntry)
	if !c.now().Before(v.expires) {
		c.order.Remove(e)
		delete(c.entries, key)
		return nil, false
	}
	c.order.MoveToFront(e)
	return append([]byte(nil), v.value...), true
}
func (c *Cache) Set(key string, value []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.capacity <= 0 {
		return
	}
	if e, ok := c.entries[key]; ok {
		c.order.Remove(e)
		delete(c.entries, key)
	}
	c.entries[key] = c.order.PushFront(cacheEntry{key, append([]byte(nil), value...), c.now().Add(c.ttl)})
	if c.order.Len() > c.capacity {
		e := c.order.Back()
		delete(c.entries, e.Value.(cacheEntry).key)
		c.order.Remove(e)
	}
}
