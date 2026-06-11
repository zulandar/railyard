package pluginhost

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/zulandar/railyard/internal/config"
	"github.com/zulandar/railyard/internal/db"
	protov1 "github.com/zulandar/railyard/pkg/plugin/proto/v1"
)

// newKVTestDB returns an in-memory SQLite GORM handle with the railyard
// schema (including plugin_kvs) migrated.
func newKVTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	gdb, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open kv test db: %v", err)
	}
	if err := db.AutoMigrate(gdb); err != nil {
		t.Fatalf("migrate kv test db: %v", err)
	}
	return gdb
}

// newKVHostService builds a hostService bound to pluginName, backed by a
// host whose Dependencies.DB is gdb.
func newKVHostService(t *testing.T, gdb *gorm.DB, pluginName string) *hostService {
	t.Helper()
	h := NewHost(Dependencies{
		Cfg: &config.Config{Owner: "tester", Project: "railyard"},
		DB:  gdb,
	})
	return newHostService(h, pluginName)
}

// TestKV_RoundTrip exercises Put/Get/Delete/List for a single plugin.
func TestKV_RoundTrip(t *testing.T) {
	ctx := context.Background()
	gdb := newKVTestDB(t)
	s := newKVHostService(t, gdb, "alpha")

	// Get a missing key: found=false, no error.
	got, err := s.KVGet(ctx, &protov1.KVGetRequest{Key: "cursor"})
	if err != nil {
		t.Fatalf("KVGet(missing): %v", err)
	}
	if got.Found {
		t.Fatal("KVGet(missing): found=true, want false")
	}

	// Put then Get.
	if _, err := s.KVPut(ctx, &protov1.KVPutRequest{Key: "cursor", Value: []byte("42")}); err != nil {
		t.Fatalf("KVPut: %v", err)
	}
	got, err = s.KVGet(ctx, &protov1.KVGetRequest{Key: "cursor"})
	if err != nil {
		t.Fatalf("KVGet: %v", err)
	}
	if !got.Found || !bytes.Equal(got.Value, []byte("42")) {
		t.Fatalf("KVGet = (%q, found=%v), want (\"42\", true)", got.Value, got.Found)
	}

	// Overwrite.
	if _, err := s.KVPut(ctx, &protov1.KVPutRequest{Key: "cursor", Value: []byte("99")}); err != nil {
		t.Fatalf("KVPut(overwrite): %v", err)
	}
	got, _ = s.KVGet(ctx, &protov1.KVGetRequest{Key: "cursor"})
	if !bytes.Equal(got.Value, []byte("99")) {
		t.Fatalf("KVGet after overwrite = %q, want \"99\"", got.Value)
	}

	// List with prefix.
	if _, err := s.KVPut(ctx, &protov1.KVPutRequest{Key: "seen:a", Value: []byte("1")}); err != nil {
		t.Fatalf("KVPut: %v", err)
	}
	if _, err := s.KVPut(ctx, &protov1.KVPutRequest{Key: "seen:b", Value: []byte("1")}); err != nil {
		t.Fatalf("KVPut: %v", err)
	}
	list, err := s.KVList(ctx, &protov1.KVListRequest{Prefix: "seen:"})
	if err != nil {
		t.Fatalf("KVList: %v", err)
	}
	if len(list.Keys) != 2 || list.Keys[0] != "seen:a" || list.Keys[1] != "seen:b" {
		t.Fatalf("KVList(prefix) = %v, want [seen:a seen:b]", list.Keys)
	}

	// List all (empty prefix) returns every key sorted.
	all, _ := s.KVList(ctx, &protov1.KVListRequest{Prefix: ""})
	if len(all.Keys) != 3 || all.Keys[0] != "cursor" {
		t.Fatalf("KVList(all) = %v, want 3 keys with cursor first", all.Keys)
	}

	// Delete then Get.
	if _, err := s.KVDelete(ctx, &protov1.KVDeleteRequest{Key: "cursor"}); err != nil {
		t.Fatalf("KVDelete: %v", err)
	}
	got, _ = s.KVGet(ctx, &protov1.KVGetRequest{Key: "cursor"})
	if got.Found {
		t.Fatal("KVGet after delete: found=true, want false")
	}

	// Deleting an absent key is a no-op (no error).
	if _, err := s.KVDelete(ctx, &protov1.KVDeleteRequest{Key: "cursor"}); err != nil {
		t.Fatalf("KVDelete(absent): %v", err)
	}
}

// TestKV_NamespaceIsolation proves two hostService instances with
// different pluginName values cannot read each other's keys: the
// namespace is the connection-bound identity, never a request field.
func TestKV_NamespaceIsolation(t *testing.T) {
	ctx := context.Background()
	gdb := newKVTestDB(t)
	alpha := newKVHostService(t, gdb, "alpha")
	beta := newKVHostService(t, gdb, "beta")

	// Both store the SAME key with different values.
	if _, err := alpha.KVPut(ctx, &protov1.KVPutRequest{Key: "shared", Value: []byte("alpha-secret")}); err != nil {
		t.Fatalf("alpha KVPut: %v", err)
	}
	if _, err := beta.KVPut(ctx, &protov1.KVPutRequest{Key: "shared", Value: []byte("beta-secret")}); err != nil {
		t.Fatalf("beta KVPut: %v", err)
	}

	// Each reads back ONLY its own value.
	ga, _ := alpha.KVGet(ctx, &protov1.KVGetRequest{Key: "shared"})
	if !ga.Found || !bytes.Equal(ga.Value, []byte("alpha-secret")) {
		t.Fatalf("alpha read %q, want alpha-secret", ga.Value)
	}
	gb, _ := beta.KVGet(ctx, &protov1.KVGetRequest{Key: "shared"})
	if !gb.Found || !bytes.Equal(gb.Value, []byte("beta-secret")) {
		t.Fatalf("beta read %q, want beta-secret", gb.Value)
	}

	// alpha stores a private key; beta must not see it via Get or List.
	if _, err := alpha.KVPut(ctx, &protov1.KVPutRequest{Key: "alpha-only", Value: []byte("x")}); err != nil {
		t.Fatalf("alpha KVPut: %v", err)
	}
	gb2, _ := beta.KVGet(ctx, &protov1.KVGetRequest{Key: "alpha-only"})
	if gb2.Found {
		t.Fatal("beta read alpha's private key: cross-plugin read leaked")
	}
	betaList, _ := beta.KVList(ctx, &protov1.KVListRequest{Prefix: ""})
	for _, k := range betaList.Keys {
		if k == "alpha-only" {
			t.Fatalf("beta KVList leaked alpha's key: %v", betaList.Keys)
		}
	}
	if len(betaList.Keys) != 1 || betaList.Keys[0] != "shared" {
		t.Fatalf("beta KVList = %v, want [shared]", betaList.Keys)
	}
}

// TestKV_RejectsOversizedValue asserts a value larger than KVMaxValueBytes
// is rejected with ResourceExhausted.
func TestKV_RejectsOversizedValue(t *testing.T) {
	ctx := context.Background()
	s := newKVHostService(t, newKVTestDB(t), "alpha")

	big := make([]byte, KVMaxValueBytes+1)
	_, err := s.KVPut(ctx, &protov1.KVPutRequest{Key: "k", Value: big})
	if status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("KVPut(oversized value) code = %v, want ResourceExhausted (err=%v)", status.Code(err), err)
	}
}

// TestKV_RejectsOversizedKey asserts a key longer than KVMaxKeyBytes is
// rejected with InvalidArgument.
func TestKV_RejectsOversizedKey(t *testing.T) {
	ctx := context.Background()
	s := newKVHostService(t, newKVTestDB(t), "alpha")

	longKey := strings.Repeat("x", KVMaxKeyBytes+1)
	_, err := s.KVPut(ctx, &protov1.KVPutRequest{Key: longKey, Value: []byte("v")})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("KVPut(oversized key) code = %v, want InvalidArgument (err=%v)", status.Code(err), err)
	}
}

// TestKV_RejectsKeyCountCap asserts that storing more than KVMaxKeys keys
// for one plugin is rejected with ResourceExhausted, while overwriting an
// existing key at the cap still succeeds.
func TestKV_RejectsKeyCountCap(t *testing.T) {
	ctx := context.Background()
	s := newKVHostService(t, newKVTestDB(t), "alpha")

	for i := 0; i < KVMaxKeys; i++ {
		key := "k" + strings.Repeat("0", 3) + itoa(i)
		if _, err := s.KVPut(ctx, &protov1.KVPutRequest{Key: key, Value: []byte("v")}); err != nil {
			t.Fatalf("KVPut #%d: %v", i, err)
		}
	}
	// One more NEW key must be rejected.
	_, err := s.KVPut(ctx, &protov1.KVPutRequest{Key: "overflow", Value: []byte("v")})
	if status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("KVPut(over cap) code = %v, want ResourceExhausted (err=%v)", status.Code(err), err)
	}
	// Overwriting an EXISTING key at the cap is still allowed (does not
	// grow the key count).
	if _, err := s.KVPut(ctx, &protov1.KVPutRequest{Key: "k000" + itoa(0), Value: []byte("updated")}); err != nil {
		t.Fatalf("KVPut(overwrite at cap): %v", err)
	}
}

// TestKV_NilDBUnavailable asserts that a host with no DB configured
// returns codes.Unavailable from every KV RPC, never panicking.
func TestKV_NilDBUnavailable(t *testing.T) {
	ctx := context.Background()
	h := NewHost(Dependencies{Cfg: &config.Config{Owner: "tester"}}) // DB == nil
	s := newHostService(h, "alpha")

	if _, err := s.KVGet(ctx, &protov1.KVGetRequest{Key: "k"}); status.Code(err) != codes.Unavailable {
		t.Errorf("KVGet code = %v, want Unavailable", status.Code(err))
	}
	if _, err := s.KVPut(ctx, &protov1.KVPutRequest{Key: "k", Value: []byte("v")}); status.Code(err) != codes.Unavailable {
		t.Errorf("KVPut code = %v, want Unavailable", status.Code(err))
	}
	if _, err := s.KVDelete(ctx, &protov1.KVDeleteRequest{Key: "k"}); status.Code(err) != codes.Unavailable {
		t.Errorf("KVDelete code = %v, want Unavailable", status.Code(err))
	}
	if _, err := s.KVList(ctx, &protov1.KVListRequest{Prefix: ""}); status.Code(err) != codes.Unavailable {
		t.Errorf("KVList code = %v, want Unavailable", status.Code(err))
	}
}

// itoa is a tiny base-10 int formatter used to build deterministic key
// names without importing strconv into the test's hot path.
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [20]byte
	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(b[pos:])
}
