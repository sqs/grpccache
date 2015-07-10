package grpccache

import (
	"crypto/sha256"
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

	Log bool
}

func cacheKey(ctx context.Context, method string, arg proto.Message) (string, error) {
	data, err := proto.Marshal(arg)
	if err != nil {
		return "", err
	}
	sha := sha256.Sum256(data)
	return method + "-" + string(sha[:]), nil
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

	cacheKey, err := cacheKey(ctx, method, arg)
	if err != nil {
		return false, err
	}

	if entry, present := c.results[cacheKey]; present {
		if time.Now().After(entry.expiry) {
			if c.Log {
				log.Printf("Cache: EXPIRED %q %+v", method, arg)
			}
			// Clear cache entry.
			delete(c.results, cacheKey)
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

	if c.Log {
		log.Printf("Cache: STORE   %q %+v: result %+v", method, arg, result)
	}
	data, err := proto.Marshal(result)
	if err != nil {
		return err
	}

	cacheKey, err := cacheKey(ctx, method, arg)
	if err != nil {
		return err
	}

	cc, err := getCacheControl(trailer)
	if err != nil {
		return err
	}

	if !cc.cacheable() {
		return nil
	}

	c.results[cacheKey] = cacheEntry{
		protoBytes: data,
		cc:         *cc,
		expiry:     time.Now().Add(cc.MaxAge),
	}
	return nil
}
