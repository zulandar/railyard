package pluginhost

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/zulandar/railyard/internal/models"
	"github.com/zulandar/railyard/pkg/plugin"
	protov1 "github.com/zulandar/railyard/pkg/plugin/proto/v1"
)

// hostServiceStore adapts a [*hostService]'s KV RPC handlers to the
// [plugin.Store] interface so the bare in-process *Host can satisfy
// [plugin.Host] by reusing the exact same namespaced KV logic the gRPC
// path uses (railyard-77h.11). The nil-DB Unavailable behaviour and all
// limit enforcement come straight from the handlers.
type hostServiceStore struct {
	svc *hostService
}

// Compile-time assertion that the adapter satisfies plugin.Store.
var _ plugin.Store = (*hostServiceStore)(nil)

func (s *hostServiceStore) Get(ctx context.Context, key string) ([]byte, bool, error) {
	resp, err := s.svc.KVGet(ctx, &protov1.KVGetRequest{Key: key})
	if err != nil {
		return nil, false, err
	}
	if !resp.Found {
		return nil, false, nil
	}
	return resp.Value, true, nil
}

func (s *hostServiceStore) Put(ctx context.Context, key string, value []byte) error {
	_, err := s.svc.KVPut(ctx, &protov1.KVPutRequest{Key: key, Value: value})
	return err
}

func (s *hostServiceStore) Delete(ctx context.Context, key string) error {
	_, err := s.svc.KVDelete(ctx, &protov1.KVDeleteRequest{Key: key})
	return err
}

func (s *hostServiceStore) List(ctx context.Context, prefix string) ([]string, error) {
	resp, err := s.svc.KVList(ctx, &protov1.KVListRequest{Prefix: prefix})
	if err != nil {
		return nil, err
	}
	return resp.Keys, nil
}

// KV store limits (railyard-77h.11). These protect the shared host DB
// from a single plugin exhausting it. They are enforced on every KVPut
// by the host-side handlers below; the SDK surfaces a violation as the
// returned gRPC error.
const (
	// KVMaxValueBytes is the largest value (in bytes) a plugin may store
	// under a single key. An over-limit value is rejected with
	// codes.ResourceExhausted.
	KVMaxValueBytes = 64 * 1024 // 64 KiB

	// KVMaxKeyBytes is the longest key (in bytes) a plugin may use. An
	// over-limit key is rejected with codes.InvalidArgument.
	KVMaxKeyBytes = 256

	// KVMaxKeys is the maximum number of distinct keys a single plugin may
	// hold at once. Storing a NEW key beyond this cap is rejected with
	// codes.ResourceExhausted; overwriting an existing key is always
	// allowed because it does not grow the namespace.
	KVMaxKeys = 1024
)

// errKVNoDB is returned (as codes.Unavailable) by every KV RPC when the
// host was constructed without a DB. Some tests construct a DB-less host;
// the RPCs must not panic.
func (s *hostService) kvDB() (*gorm.DB, error) {
	if s.host == nil || s.host.deps.DB == nil {
		return nil, status.Error(codes.Unavailable, "pluginhost: kv store not configured")
	}
	return s.host.deps.DB, nil
}

// KVGet reads one value from the calling plugin's private namespace. The
// namespace is s.pluginName (connection-bound), never a request field, so
// a plugin can only ever read its own keys (railyard-77h.11).
func (s *hostService) KVGet(ctx context.Context, req *protov1.KVGetRequest) (*protov1.KVGetResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "pluginhost: KVGet requires a request")
	}
	gdb, err := s.kvDB()
	if err != nil {
		return nil, err
	}
	var row models.PluginKV
	res := gdb.WithContext(ctx).
		Where("plugin = ? AND `key` = ?", s.pluginName, req.Key).
		Take(&row)
	if res.Error != nil {
		if errors.Is(res.Error, gorm.ErrRecordNotFound) {
			return &protov1.KVGetResponse{Found: false}, nil
		}
		return nil, status.Errorf(codes.Internal, "pluginhost: KVGet: %v", res.Error)
	}
	return &protov1.KVGetResponse{Value: row.Value, Found: true}, nil
}

// KVPut inserts or overwrites one value in the calling plugin's private
// namespace, enforcing the per-plugin limits (railyard-77h.11).
func (s *hostService) KVPut(ctx context.Context, req *protov1.KVPutRequest) (*protov1.KVPutResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "pluginhost: KVPut requires a request")
	}
	if req.Key == "" {
		return nil, status.Error(codes.InvalidArgument, "pluginhost: KVPut: key must not be empty")
	}
	if len(req.Key) > KVMaxKeyBytes {
		return nil, status.Errorf(codes.InvalidArgument,
			"pluginhost: KVPut: key length %d exceeds max %d bytes", len(req.Key), KVMaxKeyBytes)
	}
	if len(req.Value) > KVMaxValueBytes {
		return nil, status.Errorf(codes.ResourceExhausted,
			"pluginhost: KVPut: value size %d exceeds max %d bytes", len(req.Value), KVMaxValueBytes)
	}
	gdb, err := s.kvDB()
	if err != nil {
		return nil, err
	}

	// Enforce the key-count cap, but only for a NEW key. Overwriting an
	// existing key does not grow the namespace, so it is always allowed.
	// Do the check-and-write inside a single transaction so two concurrent
	// puts can't both slip past the cap.
	txErr := gdb.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var existing int64
		if err := tx.Model(&models.PluginKV{}).
			Where("plugin = ? AND `key` = ?", s.pluginName, req.Key).
			Count(&existing).Error; err != nil {
			return status.Errorf(codes.Internal, "pluginhost: KVPut: count existing: %v", err)
		}
		if existing == 0 {
			var total int64
			if err := tx.Model(&models.PluginKV{}).
				Where("plugin = ?", s.pluginName).
				Count(&total).Error; err != nil {
				return status.Errorf(codes.Internal, "pluginhost: KVPut: count keys: %v", err)
			}
			if total >= KVMaxKeys {
				return status.Errorf(codes.ResourceExhausted,
					"pluginhost: KVPut: plugin %q already holds %d keys (max %d)",
					s.pluginName, total, KVMaxKeys)
			}
		}
		row := models.PluginKV{Plugin: s.pluginName, Key: req.Key, Value: req.Value}
		// Upsert on the composite primary key so a repeated Put overwrites.
		if err := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "plugin"}, {Name: "key"}},
			DoUpdates: clause.AssignmentColumns([]string{"value", "updated_at"}),
		}).Create(&row).Error; err != nil {
			return status.Errorf(codes.Internal, "pluginhost: KVPut: write: %v", err)
		}
		return nil
	})
	if txErr != nil {
		return nil, txErr
	}
	return &protov1.KVPutResponse{}, nil
}

// KVDelete removes one key from the calling plugin's private namespace.
// Deleting an absent key is a no-op (railyard-77h.11).
func (s *hostService) KVDelete(ctx context.Context, req *protov1.KVDeleteRequest) (*protov1.KVDeleteResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "pluginhost: KVDelete requires a request")
	}
	gdb, err := s.kvDB()
	if err != nil {
		return nil, err
	}
	res := gdb.WithContext(ctx).
		Where("plugin = ? AND `key` = ?", s.pluginName, req.Key).
		Delete(&models.PluginKV{})
	if res.Error != nil {
		return nil, status.Errorf(codes.Internal, "pluginhost: KVDelete: %v", res.Error)
	}
	return &protov1.KVDeleteResponse{}, nil
}

// KVList returns the keys in the calling plugin's private namespace whose
// name begins with req.Prefix (empty prefix lists all), sorted ascending
// (railyard-77h.11).
func (s *hostService) KVList(ctx context.Context, req *protov1.KVListRequest) (*protov1.KVListResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "pluginhost: KVList requires a request")
	}
	gdb, err := s.kvDB()
	if err != nil {
		return nil, err
	}
	q := gdb.WithContext(ctx).Model(&models.PluginKV{}).
		Where("plugin = ?", s.pluginName)
	if req.Prefix != "" {
		// Escape LIKE wildcards in the user-supplied prefix so a key
		// containing % or _ does not widen the match.
		q = q.Where("`key` LIKE ? ESCAPE '\\'", escapeLikePrefix(req.Prefix)+"%")
	}
	var keys []string
	if err := q.Order("`key` ASC").Pluck("`key`", &keys).Error; err != nil {
		return nil, status.Errorf(codes.Internal, "pluginhost: KVList: %v", err)
	}
	return &protov1.KVListResponse{Keys: keys}, nil
}

// escapeLikePrefix escapes the SQL LIKE metacharacters (%, _, \) in a
// literal prefix so KVList treats the prefix verbatim.
func escapeLikePrefix(p string) string {
	var b []byte
	for i := 0; i < len(p); i++ {
		switch c := p[i]; c {
		case '\\', '%', '_':
			b = append(b, '\\', c)
		default:
			b = append(b, c)
		}
	}
	return string(b)
}
