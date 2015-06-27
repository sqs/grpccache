package grpccache

import (
	"crypto/sha256"
	"log"
	"sync"

	"github.com/gogo/protobuf/proto"
	"golang.org/x/net/context"
)

type Cache struct {
	mu      sync.RWMutex
	results map[string][]byte // method "-" sha256 of arg proto -> result proto

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

func (c *Cache) Get(ctx context.Context, method string, arg proto.Message, result proto.Message) (cached bool, err error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	cacheKey, err := cacheKey(ctx, method, arg)
	if err != nil {
		return false, err
	}

	if data, present := c.results[cacheKey]; present {
		if err := proto.Unmarshal(data, result); err != nil {
			return false, err
		}
		if c.Log {
			log.Printf("Cache: HIT   %q %+v", method, arg)
		}
		return true, nil
	}
	if c.Log {
		log.Printf("Cache: MISS  %q %+v", method, arg)
	}
	return false, nil
}

func (c *Cache) Store(ctx context.Context, method string, arg proto.Message, result proto.Message) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.results == nil {
		c.results = map[string][]byte{}
	}

	if c.Log {
		log.Printf("Cache: STORE %q %+v", method, arg)
	}
	data, err := proto.Marshal(result)
	if err != nil {
		return err
	}

	cacheKey, err := cacheKey(ctx, method, arg)
	if err != nil {
		return err
	}

	c.results[cacheKey] = data
	return nil
}
