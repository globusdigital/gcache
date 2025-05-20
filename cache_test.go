package gcache

import (
	"bytes"
	"context"
	"encoding/gob"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestLoaderFunc(t *testing.T) {
	size := 2
	testCaches := []*CacheBuilder[int, int]{
		New[int, int](size).Simple(),
		New[int, int](size).LRU(),
		New[int, int](size).LFU(),
		New[int, int](size).ARC(),
	}
	for _, builder := range testCaches {
		var testCounter int64
		counter := 1000
		cache := builder.
			LoaderFunc(func(ctx context.Context, key int) (int, error) {
				time.Sleep(10 * time.Millisecond)
				return int(atomic.AddInt64(&testCounter, 1)), nil
			}).
			EvictedFunc(func(key, value int) {
				panic(key)
			}).Build()

		var wg sync.WaitGroup
		for i := 0; i < counter; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, err := cache.Get(0)
				if err != nil {
					t.Error(err)
				}
			}()
		}
		wg.Wait()

		if testCounter != 1 {
			t.Errorf("testCounter != %v", testCounter)
		}
	}
}

func TestLoaderExpireFuncWithoutExpire(t *testing.T) {
	size := 2
	testCaches := []*CacheBuilder[int, int]{
		New[int, int](size).Simple(),
		New[int, int](size).LRU(),
		New[int, int](size).LFU(),
		New[int, int](size).ARC(),
	}
	for _, builder := range testCaches {
		var testCounter int64
		counter := 1000
		cache := builder.
			LoaderExpireFunc(func(ctx context.Context, key int) (int, *time.Duration, error) {
				return int(atomic.AddInt64(&testCounter, 1)), nil, nil
			}).
			EvictedFunc(func(key, value int) {
				panic(key)
			}).Build()

		var wg sync.WaitGroup
		for i := 0; i < counter; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, err := cache.Get(0)
				if err != nil {
					t.Error(err)
				}
			}()
		}

		wg.Wait()

		if testCounter != 1 {
			t.Errorf("testCounter != %v", testCounter)
		}
	}
}

func TestLoaderExpireFuncWithExpire(t *testing.T) {
	size := 2
	testCaches := []*CacheBuilder[int, int]{
		New[int, int](size).Simple(),
		New[int, int](size).LRU(),
		New[int, int](size).LFU(),
		New[int, int](size).ARC(),
	}
	for _, builder := range testCaches {
		var testCounter int64
		counter := 1000
		expire := 200 * time.Millisecond
		cache := builder.
			LoaderExpireFunc(func(ctx context.Context, key int) (int, *time.Duration, error) {
				return int(atomic.AddInt64(&testCounter, 1)), &expire, nil
			}).
			Build()

		var wg sync.WaitGroup
		for i := 0; i < counter; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, err := cache.Get(0)
				if err != nil {
					t.Error(err)
				}
			}()
		}
		time.Sleep(expire) // Waiting for key expiration
		for i := 0; i < counter; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, err := cache.Get(0)
				if err != nil {
					t.Error(err)
				}
			}()
		}

		wg.Wait()

		if testCounter != 2 {
			t.Errorf("testCounter != %v", testCounter)
		}
	}
}

func TestLoaderPurgeVisitorFunc(t *testing.T) {
	size := 7
	tests := []struct {
		name         string
		cacheBuilder *CacheBuilder[int64, int64]
	}{
		{
			name:         "simple",
			cacheBuilder: New[int64, int64](size).Simple(),
		},
		{
			name:         "lru",
			cacheBuilder: New[int64, int64](size).LRU(),
		},
		{
			name:         "lfu",
			cacheBuilder: New[int64, int64](size).LFU(),
		},
		{
			name:         "arc",
			cacheBuilder: New[int64, int64](size).ARC(),
		},
	}

	for _, test := range tests {
		var purgeCounter, evictCounter, loaderCounter int64
		counter := 1000
		cache := test.cacheBuilder.
			LoaderFunc(func(ctx context.Context, key int64) (int64, error) {
				return atomic.AddInt64(&loaderCounter, 1), nil
			}).
			EvictedFunc(func(key, value int64) {
				atomic.AddInt64(&evictCounter, 1)
			}).
			PurgeVisitorFunc(func(k, v int64) {
				atomic.AddInt64(&purgeCounter, 1)
			}).
			Build()

		var wg sync.WaitGroup
		for i := 0; i < counter; i++ {
			i := i
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, err := cache.Get(int64(i))
				if err != nil {
					t.Error(err)
				}
			}()
		}

		wg.Wait()

		if loaderCounter != int64(counter) {
			t.Errorf("%s: loaderCounter != %v", test.name, loaderCounter)
		}

		cache.Purge()

		if evictCounter+purgeCounter != loaderCounter {
			t.Logf("%s: evictCounter: %d", test.name, evictCounter)
			t.Logf("%s: purgeCounter: %d", test.name, purgeCounter)
			t.Logf("%s: loaderCounter: %d", test.name, loaderCounter)
			t.Errorf("%s: load != evict+purge", test.name)
		}
	}
}

func TestDeserializeFunc(t *testing.T) {
	cases := []struct {
		tp string
	}{
		{TYPE_SIMPLE},
		{TYPE_LRU},
		{TYPE_LFU},
		{TYPE_ARC},
	}

	for _, cs := range cases {
		key1, value1 := "key1", "value1"
		key2, value2 := "key2", "value2"
		cc := New[string, string](32).
			EvictType(cs.tp).
			LoaderFunc(func(ctx context.Context, k string) (string, error) {
				return value1, nil
			}).
			DeserializeFunc(func(k, v string) (string, error) {
				dec := gob.NewDecoder(strings.NewReader(v))
				var str string
				err := dec.Decode(&str)
				if err != nil {
					return "", err
				}
				return str, nil
			}).
			SerializeFunc(func(k, v string) (string, error) {
				buf := new(bytes.Buffer)
				enc := gob.NewEncoder(buf)
				err := enc.Encode(v)
				return buf.String(), err
			}).
			Build()
		v, err := cc.Get(key1)
		if err != nil {
			t.Fatal(err)
		}
		if v != value1 {
			t.Errorf("%v != %v", v, value1)
		}
		v, err = cc.Get(key1)
		if err != nil {
			t.Fatal(err)
		}
		if v != value1 {
			t.Errorf("%v != %v", v, value1)
		}
		if err := cc.Set(key2, value2); err != nil {
			t.Error(err)
		}
		v, err = cc.Get(key2)
		if err != nil {
			t.Error(err)
		}
		if v != value2 {
			t.Errorf("%v != %v", v, value2)
		}
	}
}

func TestExpiredItems(t *testing.T) {
	tps := []string{
		TYPE_SIMPLE,
		TYPE_LRU,
		TYPE_LFU,
		TYPE_ARC,
	}
	for _, tp := range tps {
		t.Run(tp, func(t *testing.T) {
			testExpiredItems(t, tp)
		})
	}
}
