package plugin

import (
	"bytes"
	"context"
	"errors"
	"testing"

	protov1 "github.com/zulandar/railyard/pkg/plugin/proto/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// fakeKVHostClient is a minimal HostServiceClient that records and serves
// the four KV RPCs from an in-memory map. Every other HostService method
// is left unimplemented (embedded interface) because these tests never
// invoke them.
type fakeKVHostClient struct {
	protov1.HostServiceClient
	store map[string][]byte
	// putErr, when set, is returned from KVPut to exercise the SDK's
	// error surfacing.
	putErr error
}

func (f *fakeKVHostClient) KVGet(_ context.Context, req *protov1.KVGetRequest, _ ...grpc.CallOption) (*protov1.KVGetResponse, error) {
	v, ok := f.store[req.Key]
	return &protov1.KVGetResponse{Value: v, Found: ok}, nil
}

func (f *fakeKVHostClient) KVPut(_ context.Context, req *protov1.KVPutRequest, _ ...grpc.CallOption) (*protov1.KVPutResponse, error) {
	if f.putErr != nil {
		return nil, f.putErr
	}
	if f.store == nil {
		f.store = make(map[string][]byte)
	}
	f.store[req.Key] = req.Value
	return &protov1.KVPutResponse{}, nil
}

func (f *fakeKVHostClient) KVDelete(_ context.Context, req *protov1.KVDeleteRequest, _ ...grpc.CallOption) (*protov1.KVDeleteResponse, error) {
	delete(f.store, req.Key)
	return &protov1.KVDeleteResponse{}, nil
}

func (f *fakeKVHostClient) KVList(_ context.Context, req *protov1.KVListRequest, _ ...grpc.CallOption) (*protov1.KVListResponse, error) {
	var keys []string
	for k := range f.store {
		if req.Prefix == "" || len(k) >= len(req.Prefix) && k[:len(req.Prefix)] == req.Prefix {
			keys = append(keys, k)
		}
	}
	return &protov1.KVListResponse{Keys: keys}, nil
}

// TestHostClientStoreRoundTrip proves the SDK Store accessor maps each
// method onto the matching KV RPC and decodes the response.
func TestHostClientStoreRoundTrip(t *testing.T) {
	t.Parallel()
	fake := &fakeKVHostClient{store: map[string][]byte{}}
	hc := newHostClient("alpha", fake, context.Background())
	ctx := context.Background()

	st := hc.Store()
	if st == nil {
		t.Fatal("Store() returned nil")
	}

	// Missing key.
	if _, found, err := st.Get(ctx, "cursor"); err != nil || found {
		t.Fatalf("Get(missing) = (found=%v, err=%v), want (false, nil)", found, err)
	}

	// Put then Get.
	if err := st.Put(ctx, "cursor", []byte("42")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	v, found, err := st.Get(ctx, "cursor")
	if err != nil || !found || !bytes.Equal(v, []byte("42")) {
		t.Fatalf("Get = (%q, found=%v, err=%v), want (\"42\", true, nil)", v, found, err)
	}

	// List.
	_ = st.Put(ctx, "seen:a", []byte("1"))
	keys, err := st.List(ctx, "seen:")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(keys) != 1 || keys[0] != "seen:a" {
		t.Fatalf("List(seen:) = %v, want [seen:a]", keys)
	}

	// Delete.
	if err := st.Delete(ctx, "cursor"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, found, _ := st.Get(ctx, "cursor"); found {
		t.Fatal("Get after Delete: found=true, want false")
	}
}

// TestHostClientStorePutErrorSurfaces proves a host-side limit rejection
// (a gRPC error) is surfaced from Store.Put rather than swallowed.
func TestHostClientStorePutErrorSurfaces(t *testing.T) {
	t.Parallel()
	fake := &fakeKVHostClient{
		store:  map[string][]byte{},
		putErr: status.Error(codes.ResourceExhausted, "value too large"),
	}
	hc := newHostClient("alpha", fake, context.Background())

	err := hc.Store().Put(context.Background(), "k", []byte("v"))
	if err == nil {
		t.Fatal("Put: expected error from host-side rejection, got nil")
	}
	if status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("Put error code = %v, want ResourceExhausted (err=%v)", status.Code(err), err)
	}
}

// TestHostClientStorePutErrorWrapped tolerates either a raw gRPC status or
// a wrapped error, as long as the underlying status code is preserved.
func TestHostClientStorePutErrorWrapped(t *testing.T) {
	t.Parallel()
	sentinel := status.Error(codes.InvalidArgument, "key too long")
	fake := &fakeKVHostClient{store: map[string][]byte{}, putErr: sentinel}
	hc := newHostClient("alpha", fake, context.Background())

	err := hc.Store().Put(context.Background(), "k", []byte("v"))
	if !errors.Is(err, sentinel) && status.Code(err) != codes.InvalidArgument {
		t.Fatalf("Put error = %v; want it to preserve InvalidArgument", err)
	}
}
