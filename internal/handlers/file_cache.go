package handlers

import (
	"os"
	"sync"
	"time"
)

// fileCache keeps a parsed file until its mtime or size changes, ok is false when the file is gone
type fileCache[T any] struct {
	mu sync.Mutex
	m  map[string]fileCacheEntry[T]
}

type fileCacheEntry[T any] struct {
	mod  time.Time
	size int64
	val  T
}

func (c *fileCache[T]) get(path string, parse func(path string) T) (val T, ok bool) {
	info, err := os.Stat(path)
	c.mu.Lock()
	if c.m == nil {
		c.m = map[string]fileCacheEntry[T]{}
	}
	if err != nil {
		delete(c.m, path)
		c.mu.Unlock()
		return val, false
	}
	e, hit := c.m[path]
	c.mu.Unlock()
	if hit && e.mod.Equal(info.ModTime()) && e.size == info.Size() {
		return e.val, true
	}

	val = parse(path)
	c.mu.Lock()
	c.m[path] = fileCacheEntry[T]{mod: info.ModTime(), size: info.Size(), val: val}
	c.mu.Unlock()
	return val, true
}

// ttlCache keeps one value for ttl, once it has a value a stale read returns it right away and refreshes in the background
type ttlCache[T any] struct {
	ttl        time.Duration
	mu         sync.Mutex
	at         time.Time
	val        T
	refreshing bool
}

func (c *ttlCache[T]) get(load func() T) T {
	c.mu.Lock()
	if c.at.IsZero() {
		c.mu.Unlock()
		v := load()
		c.set(v)
		return v
	}
	v := c.val
	if time.Since(c.at) >= c.ttl && !c.refreshing {
		c.refreshing = true
		go func() { c.set(load()) }()
	}
	c.mu.Unlock()
	return v
}

func (c *ttlCache[T]) set(v T) {
	c.mu.Lock()
	c.val, c.at, c.refreshing = v, time.Now(), false
	c.mu.Unlock()
}

// reset forgets the value so the next get loads it again
func (c *ttlCache[T]) reset() {
	c.mu.Lock()
	c.at = time.Time{}
	c.mu.Unlock()
}
