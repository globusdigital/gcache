package gcache

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

const (
	TYPE_SIMPLE = "simple"
	TYPE_LRU    = "lru"
	TYPE_LFU    = "lfu"
	TYPE_ARC    = "arc"
)

var KeyNotFoundError = errors.New("key not found")

type Cache[K comparable, V any] interface {
	// Set inserts or updates the specified key-value pair.
	Set(key K, value V) error
	// SetWithExpire inserts or updates the specified key-value pair with an
	// expiration time.
	SetWithExpire(key K, value V, expiration time.Duration) error
	// Get returns the value for the specified key if it is present in the
	// cache. If the key is not present in the cache and the cache has
	// LoaderFunc, invoke the `LoaderFunc` function and inserts the key-value
	// pair in the cache. If the key is not present in the cache and the cache
	// does not have a LoaderFunc, return KeyNotFoundError.
	Get(K) (V, error)
	// GetIFPresent returns the value for the specified key if it is present in
	// the cache. Return KeyNotFoundError if the key is not present.
	GetIFPresent(K) (V, error)
	GetWithContext(context.Context, K) (V, error)
	GetIFPresentWithContext(context.Context, K) (V, error)
	// GetALL returns a map containing all key-value pairs in the cache.
	GetALL(checkExpired bool) map[K]V
	get(key K, onLoad bool) (V, error)
	// Remove removes the specified key from the cache if the key is present.
	// Returns true if the key was present and the key has been deleted.
	Remove(key K) bool
	// Purge removes all key-value pairs from the cache.
	Purge()
	// Keys returns a slice containing all keys in the cache.
	Keys(checkExpired bool) []K
	// Len returns the number of items in the cache.
	Len(checkExpired bool) int
	// Has returns true if the key exists in the cache.
	Has(key K) bool

	statsAccessor
}

type baseCache[K comparable, V any] struct {
	clock            Clock
	size             int
	loaderExpireFunc LoaderExpireFunc[K, V]
	evictedFunc      EvictedFunc[K, V]
	purgeVisitorFunc PurgeVisitorFunc[K, V]
	addedFunc        AddedFunc[K, V]
	deserializeFunc  DeserializeFunc[K, V]
	serializeFunc    SerializeFunc[K, V]
	expiration       *time.Duration
	mu               sync.RWMutex
	loadGroup        Group[K, V]
	*stats
}

type (
	LoaderFunc[K comparable, V any]       func(context.Context, K) (V, error)
	LoaderExpireFunc[K comparable, V any] func(context.Context, K) (V, *time.Duration, error)
	EvictedFunc[K comparable, V any]      func(K, V)
	PurgeVisitorFunc[K comparable, V any] func(K, V)
	AddedFunc[K comparable, V any]        func(K, V)
	DeserializeFunc[K comparable, V any]  func(K, V) (V, error)
	SerializeFunc[K comparable, V any]    func(K, V) (V, error)
)

type CacheBuilder[K comparable, V any] struct {
	clock            Clock
	tp               string
	size             int
	loaderExpireFunc LoaderExpireFunc[K, V]
	evictedFunc      EvictedFunc[K, V]
	purgeVisitorFunc PurgeVisitorFunc[K, V]
	addedFunc        AddedFunc[K, V]
	expiration       *time.Duration
	deserializeFunc  DeserializeFunc[K, V]
	serializeFunc    SerializeFunc[K, V]
}

func New[K comparable, V any](size int) *CacheBuilder[K, V] {
	return &CacheBuilder[K, V]{
		clock: NewRealClock(),
		tp:    TYPE_SIMPLE,
		size:  size,
	}
}

func (cb *CacheBuilder[K, V]) Clock(clock Clock) *CacheBuilder[K, V] {
	cb.clock = clock
	return cb
}

// LoaderFunc Set a loader function. loaderFunc: create a new value with this
// function if cached value is expired.
func (cb *CacheBuilder[K, V]) LoaderFunc(loaderFunc LoaderFunc[K, V]) *CacheBuilder[K, V] {
	cb.loaderExpireFunc = func(ctx context.Context, k K) (V, *time.Duration, error) {
		v, err := loaderFunc(ctx, k)
		return v, nil, err
	}
	return cb
}

// LoaderExpireFunc Set a loader function with expiration. loaderExpireFunc:
// create a new value with this function if cached value is expired. If nil
// returned instead of time.Duration from loaderExpireFunc than value will never
// expire.
func (cb *CacheBuilder[K, V]) LoaderExpireFunc(loaderExpireFunc LoaderExpireFunc[K, V]) *CacheBuilder[K, V] {
	cb.loaderExpireFunc = loaderExpireFunc
	return cb
}

func (cb *CacheBuilder[K, V]) EvictType(tp string) *CacheBuilder[K, V] {
	cb.tp = tp
	return cb
}

func (cb *CacheBuilder[K, V]) Simple() *CacheBuilder[K, V] {
	return cb.EvictType(TYPE_SIMPLE)
}

func (cb *CacheBuilder[K, V]) LRU() *CacheBuilder[K, V] {
	return cb.EvictType(TYPE_LRU)
}

func (cb *CacheBuilder[K, V]) LFU() *CacheBuilder[K, V] {
	return cb.EvictType(TYPE_LFU)
}

func (cb *CacheBuilder[K, V]) ARC() *CacheBuilder[K, V] {
	return cb.EvictType(TYPE_ARC)
}

func (cb *CacheBuilder[K, V]) EvictedFunc(evictedFunc EvictedFunc[K, V]) *CacheBuilder[K, V] {
	cb.evictedFunc = evictedFunc
	return cb
}

func (cb *CacheBuilder[K, V]) PurgeVisitorFunc(purgeVisitorFunc PurgeVisitorFunc[K, V]) *CacheBuilder[K, V] {
	cb.purgeVisitorFunc = purgeVisitorFunc
	return cb
}

func (cb *CacheBuilder[K, V]) AddedFunc(addedFunc AddedFunc[K, V]) *CacheBuilder[K, V] {
	cb.addedFunc = addedFunc
	return cb
}

func (cb *CacheBuilder[K, V]) DeserializeFunc(deserializeFunc DeserializeFunc[K, V]) *CacheBuilder[K, V] {
	cb.deserializeFunc = deserializeFunc
	return cb
}

func (cb *CacheBuilder[K, V]) SerializeFunc(serializeFunc SerializeFunc[K, V]) *CacheBuilder[K, V] {
	cb.serializeFunc = serializeFunc
	return cb
}

func (cb *CacheBuilder[K, V]) Expiration(expiration time.Duration) *CacheBuilder[K, V] {
	cb.expiration = &expiration
	return cb
}

func (cb *CacheBuilder[K, V]) Build() Cache[K, V] {
	if cb.size <= 0 && cb.tp != TYPE_SIMPLE {
		panic("gcache: Cache size <= 0")
	}

	return cb.build()
}

func (cb *CacheBuilder[K, V]) build() Cache[K, V] {
	switch cb.tp {
	case TYPE_SIMPLE:
		return newSimpleCache[K, V](cb)
	case TYPE_LRU:
		return newLRUCache[K, V](cb)
	case TYPE_LFU:
		return newLFUCache[K, V](cb)
	case TYPE_ARC:
		return newARC[K, V](cb)
	default:
		panic("gcache: Unknown type " + cb.tp)
	}
}

func buildCache[K comparable, V any](c *baseCache[K, V], cb *CacheBuilder[K, V]) {
	c.clock = cb.clock
	c.size = cb.size
	c.loaderExpireFunc = cb.loaderExpireFunc
	c.expiration = cb.expiration
	c.addedFunc = cb.addedFunc
	c.deserializeFunc = cb.deserializeFunc
	c.serializeFunc = cb.serializeFunc
	c.evictedFunc = cb.evictedFunc
	c.purgeVisitorFunc = cb.purgeVisitorFunc
	c.stats = &stats{}
}

// load a new value using by specified key.
func (c *baseCache[K, V]) load(ctx context.Context, key K, cb func(V, *time.Duration, error) (V, error), isWait bool) (V, bool, error) {
	v, called, err := c.loadGroup.Do(key, func() (v V, e error) {
		defer func() {
			if r := recover(); r != nil {
				e = fmt.Errorf("loader panics: %v", r)
			}
		}()
		return cb(c.loaderExpireFunc(ctx, key))
	}, isWait)
	if err != nil {
		var v V
		return v, called, err
	}
	return v, called, nil
}
