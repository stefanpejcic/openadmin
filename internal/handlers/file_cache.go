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
