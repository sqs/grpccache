package grpccache

import (
	"crypto/sha256"
	"encoding/base64"
	"log"
	"sync"
	"time"

	"github.com/gogo/protobuf/proto"
	"golang.org/x/net/context"
	"google.golang.org/grpc/metadata"
)

type cacheEntry struct {
	protoBytes []byte
	cc         CacheControl
	expiry     time.Time
}

// A Cache holds and allows retrieval of gRPC method call results that
// a client has previously seen.
type Cache struct {
	mu      sync.Mutex
	results map[string]cacheEntry // method "-" sha256 of arg proto -> cache entry

	// MaxSize is the maximum size, in bytes, that this cache will
	// store. An item is not stored if storing it would cause the
	// cache size to exceed MaxSize.
	MaxSize uint64
	size    uint64 // current size

	// KeyPart, if non-nil, returns a string that is appended to the
	// key. It can be used to ensure that items from separate users,
	// for example, are not comingled.
	KeyPart func(ctx context.Context) string

	Log bool
}

func (c *Cache) cacheKey(ctx context.Context, method string, arg proto.Message) (string, error) {
	data, err := proto.Marshal(arg)
	if err != nil {
		return "", err
	}
	sha := sha256.Sum256(data)
	s := method + "-" + base64.StdEncoding.EncodeToString(sha[:])

	if c.KeyPart != nil {
		s += "-" + c.KeyPart(ctx)
	}

	return s, nil
}

// Get retrieves a cached result for a gRPC method call (on the
// client), if it exists in the cache. It is called from
// CachedXyzClient auto-generated wrapper methods.
//
// The `method` and `arg` parameters are for the call that's in
// progress. If a cached result is found (that has not expired), it is
// written to the `result` parameter and (true, nil) is returned. If
// there's no cached result (or it has expired), then (false, nil) is
// returned. Otherwise a non-nil error is returned.
func (c *Cache) Get(ctx context.Context, method string, arg proto.Message, result proto.Message) (cached bool, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	cacheKey, err := c.cacheKey(ctx, method, arg)
	if err != nil {
		return false, err
	}

	if entry, present := c.results[cacheKey]; present {
		if time.Now().After(entry.expiry) {
			// Clear cache entry.
			delete(c.results, cacheKey)
			c.size -= uint64(len(entry.protoBytes))

			if c.Log {
				log.Printf("Cache: EXPIRED %q %+v (size %d)", method, arg, c.size)
			}
			return false, nil
		}
		if err := proto.Unmarshal(entry.protoBytes, result); err != nil {
			return false, err
		}
		if c.Log {
			log.Printf("Cache: HIT     %q %+v: result %+v", method, arg, result)
		}
		return true, nil
	}
	if c.Log {
		log.Printf("Cache: MISS    %q %+v", method, arg)
	}
	return false, nil
}

// Store records the result from a gRPC method call. It is called by
// the CachedXyzClient auto-generated wrapper methods.
func (c *Cache) Store(ctx context.Context, method string, arg proto.Message, result proto.Message, trailer metadata.MD) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.results == nil {
		c.results = map[string]cacheEntry{}
	}

	data, err := proto.Marshal(result)
	if err != nil {
		return err
	}

	cacheKey, err := c.cacheKey(ctx, method, arg)
	if err != nil {
		return err
	}

	cc, err := getCacheControl(trailer)
	if err != nil {
		return err
	}

	if cc == nil || !cc.cacheable() {
		return nil
	}

	afterSize := c.size
	if prev, ok := c.results[cacheKey]; ok {
		afterSize -= uint64(len(prev.protoBytes))
	}
	afterSize += uint64(len(data))
	if c.MaxSize != 0 && afterSize > c.MaxSize {
		if _, ok := c.results[cacheKey]; ok {
			// Delete it because it's probably stale anyway.
			delete(c.results, cacheKey)
			c.size -= uint64(len(c.results[cacheKey].protoBytes))
		}
		return nil
	}

	c.results[cacheKey] = cacheEntry{
		protoBytes: data,
		cc:         *cc,
		expiry:     time.Now().Add(cc.MaxAge),
	}
	c.size = afterSize

	if c.Log {
		log.Printf("Cache: STORE   %q %+v: result %+v (size %d)", method, arg, result, c.size)
	}
	return nil
}

// Clear removes all items from the cache.
func (c *Cache) Clear() {
	c.mu.Lock()
	c.results = map[string]cacheEntry{}
	c.size = 0
	c.mu.Unlock()
}
