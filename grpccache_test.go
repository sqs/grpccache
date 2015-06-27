package grpccache_test

import (
	"net"
	"reflect"
	"testing"

	"sourcegraph.com/sqs/grpccache/testpb"

	"golang.org/x/net/context"
	"google.golang.org/grpc"
)

func TestGRPCCache(t *testing.T) {
	l, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatal(err)
	}

	var ts testServer
	gs := grpc.NewServer()
	testpb.RegisterTestServer(gs, &ts)
	go func() {
		if err := gs.Serve(l); err != nil {
			t.Fatal(err)
		}
	}()
	defer gs.Stop()

	cc, err := grpc.Dial(l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	c := &testpb.CachedTestClient{TestClient: testpb.NewTestClient(cc)}
	c.Cache.Log = true

	ctx := context.Background()

	if want := 0; len(ts.calls) != want {
		t.Errorf("got %d calls (%+v), want %d", len(ts.calls), ts.calls, want)
	}

	testNotCached := func(op *testpb.TestOp) {
		beforeNumCalls := len(ts.calls)
		r, err := c.TestMethod(ctx, op)
		if err != nil {
			t.Fatal(err)
		}
		if want := (&testpb.TestResult{X: op.A}); !reflect.DeepEqual(r, want) {
			t.Errorf("got %#v, want %#v", r, want)
		}
		if want := beforeNumCalls + 1; len(ts.calls) != want {
			t.Errorf("server did not handle call %+v (client handled it from cache), wanted it to be uncached", op)
		}
	}

	testCached := func(op *testpb.TestOp) {
		beforeNumCalls := len(ts.calls)
		r, err := c.TestMethod(ctx, op)
		if err != nil {
			t.Fatal(err)
		}
		if want := (&testpb.TestResult{X: op.A}); !reflect.DeepEqual(r, want) {
			t.Errorf("got %#v, want %#v", r, want)
		}
		if want := beforeNumCalls; len(ts.calls) != want {
			t.Errorf("server handled call %+v, wanted it to be client-cached", op)
		}
	}

	testNotCached(&testpb.TestOp{A: 1})
	testCached(&testpb.TestOp{A: 1})
	testNotCached(&testpb.TestOp{A: 2})
	testNotCached(&testpb.TestOp{A: 2, B: []*testpb.T{{A: true}}})
	testCached(&testpb.TestOp{A: 2})
	testCached(&testpb.TestOp{A: 2, B: []*testpb.T{{A: true}}})
	testCached(&testpb.TestOp{A: 1})
	testNotCached(&testpb.TestOp{A: 3})
}

type testServer struct {
	calls []*testpb.TestOp
}

func (s *testServer) TestMethod(ctx context.Context, op *testpb.TestOp) (*testpb.TestResult, error) {
	s.calls = append(s.calls, op)
	return &testpb.TestResult{X: op.A}, nil
}
