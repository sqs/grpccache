package grpccache

import (
	"time"

	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

// CacheControl is passed by the CachedXyzServer wrapper to the
// underlying server's method implementation to allow control over the
// duration and nature of caching on a per-request basis.
type CacheControl struct {
	// MaxAge is maximum duration (since the original retrieval) that
	// an item is considered fresh.
	MaxAge time.Duration
}

func (cc *CacheControl) cacheable() bool {
	return cc.MaxAge > 0
}

// SetCacheControl is called by gRPC server method implementations to
// tell the client how to cache the result. It writes a gRPC header
// and/or trailer to communicate the cache control info to the client.
//
// It may be called at most once per unary RPC handler (which is a
// constraint imposed by gRPC; see the grpc.SendHeader and
// grpc.SetTrailer docs).
func SetCacheControl(ctx context.Context, cc CacheControl) error {
	return grpc.SetTrailer(ctx, metadata.MD{"cache-control:max-age": cc.MaxAge.String()})
}

func getCacheControl(md metadata.MD) (*CacheControl, error) {
	var cc *CacheControl
	if maxAgeStr, present := md["cache-control:max-age"]; present {
		maxAge, err := time.ParseDuration(maxAgeStr)
		if err != nil {
			return nil, err
		}
		if cc == nil {
			cc = new(CacheControl)
		}
		cc.MaxAge = maxAge
	}
	return cc, nil
}
