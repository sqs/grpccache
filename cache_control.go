package grpccache

import (
	"fmt"
	"strconv"
	"time"

	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

// CacheControl is passed by the CachedXyzServer wrapper to the
// underlying server's method implementation to allow control over the
// duration and nature of caching on a per-request basis.
type CacheControl struct {
	// TODO(sqs): use max-age and set expiration date based on
	// client/server's own clock -- this impl is highly dependent on
	// clock sync between client and server.
	Expires time.Time
}

// SetCacheControl is called by gRPC server method implementations to
// tell the client how to cache the result. It writes a gRPC header
// and/or trailer to communicate the cache control info to the client.
//
// It may be called at most once per unary RPC handler (which is a
// constraint imposed by gRPC; see the grpc.SendHeader and
// grpc.SetTrailer docs).
func SetCacheControl(ctx context.Context, cc CacheControl) error {
	return grpc.SetTrailer(ctx, metadata.MD{"cache-expires": fmt.Sprint(cc.Expires.UnixNano())})
}

func getCacheControl(md metadata.MD) (*CacheControl, error) {
	if expiresStr, present := md["cache-expires"]; present {
		nano, err := strconv.ParseInt(expiresStr, 10, 64)
		if err != nil {
			return nil, err
		}
		expires := time.Unix(0, nano)
		return &CacheControl{Expires: expires}, nil
	}
	return nil, nil
}

// type contextKey int

// const cacheControlKey contextKey = iota

// // CacheControlFromContext returns the CacheControl previously set by
// // withCacheControl. gRPC server method implementations can use it to
// // retrieve the CacheControl set by their CachedXyzServer wrapper. If
// // no CacheControl exists in the context, it panics.
// func CacheControlFromContext(ctx context.Context) *CacheControl {
// 	cc := ctx.Value(cacheControlKey)
// 	if cc == nil {
// 		panic("no CacheControl set in context")
// 	}
// 	return cc.(*CacheControl)
// }

// func withCacheControl(ctx context.Context, cc *CacheControl) context.Context {
// 	return context.WithValue(ctx, cacheControlKey, cc)
// }
