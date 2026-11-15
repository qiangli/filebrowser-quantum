// Package cache is a small in-process key/value cache with per-entry expiry,
// kept in this module so the backend depends on no external cache library.
//
// NewCache(expiry, cleanup) returns a cache whose entries expire after
// `expiry` (default 24 h) and whose expired entries are swept every
// `cleanup` (default 1 h, plus one to two seconds of jitter so many caches
// never sweep in lock-step). Get never returns an expired value.
package cache

import (
	"math/rand/v2"
	"sync"
	"time"
)

// KeyCache maps string keys to values of type T with a deadline per entry.
type KeyCache[T any] struct {
	mu           sync.RWMutex
	data         map[string]entry[T]
	expiresAfter time.Duration
}

type entry[T any] struct {
	value     T
	expiresAt time.Time
}

// NewCache builds a cache. settings[0] is the default expiry, settings[1]
// the sweep interval; both are optional.
func NewCache[T any](settings ...time.Duration) *KeyCache[T] {
	expires, cleanup := 24*time.Hour, time.Hour
	if len(settings) > 0 && settings[0] > 0 {
		expires = settings[0]
	}
	if len(settings) > 1 && settings[1] > 0 {
		cleanup = settings[1]
	}
	c := &KeyCache[T]{data: make(map[string]entry[T]), expiresAfter: expires}
	jitter := time.Second + time.Duration(rand.Int64N(int64(time.Second)))
	go c.sweep(cleanup + jitter)
	return c
}

// Set stores value under key with the cache's default expiry.
func (c *KeyCache[T]) Set(key string, value T) { c.SetWithExp(key, value, c.expiresAfter) }

// SetWithExp stores value under key, expiring after exp.
func (c *KeyCache[T]) SetWithExp(key string, value T, exp time.Duration) {
	c.mu.Lock()
	c.data[key] = entry[T]{value: value, expiresAt: time.Now().Add(exp)}
	c.mu.Unlock()
}

// Get returns the live value for key and whether one exists.
func (c *KeyCache[T]) Get(key string) (T, bool) {
	c.mu.RLock()
	e, ok := c.data[key]
	c.mu.RUnlock()
	if !ok || time.Now().After(e.expiresAt) {
		var zero T
		return zero, false
	}
	return e.value, true
}

// Delete removes key.
func (c *KeyCache[T]) Delete(key string) {
	c.mu.Lock()
	delete(c.data, key)
	c.mu.Unlock()
}

// ClearAll removes every entry.
func (c *KeyCache[T]) ClearAll() {
	c.mu.Lock()
	c.data = make(map[string]entry[T])
	c.mu.Unlock()
}

func (c *KeyCache[T]) sweep(every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for range t.C {
		now := time.Now()
		c.mu.Lock()
		for k, e := range c.data {
			if now.After(e.expiresAt) {
				delete(c.data, k)
			}
		}
		c.mu.Unlock()
	}
}
