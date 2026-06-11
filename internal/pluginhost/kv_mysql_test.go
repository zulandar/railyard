package pluginhost

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/zulandar/railyard/internal/config"
	"github.com/zulandar/railyard/internal/models"
	protov1 "github.com/zulandar/railyard/pkg/plugin/proto/v1"
)

// newMySQLCaptureHostService builds a hostService backed by a GORM handle
// using the *MySQL* dialect over a sqlmock connection that records every
// SQL string the KV handlers generate. Unlike the SQLite-backed tests,
// this exercises MySQL identifier quoting — the dialect production actually
// runs (internal/db/connect.go opens MySQL only). The returned *[]string is
// appended to as queries are issued.
func newMySQLCaptureHostService(t *testing.T, pluginName string) (*hostService, sqlmock.Sqlmock, *[]string) {
	t.Helper()

	captured := &[]string{}
	matcher := sqlmock.QueryMatcherFunc(func(_ string, actualSQL string) error {
		*captured = append(*captured, actualSQL)
		return nil // record-only: matching is asserted in Go, not by sqlmock
	})

	sqlDB, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(matcher))
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	gdb, err := gorm.Open(mysql.New(mysql.Config{
		Conn:                      sqlDB,
		SkipInitializeWithVersion: true,
	}), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("gorm.Open(mysql): %v", err)
	}

	h := NewHost(Dependencies{
		Cfg: &config.Config{Owner: "tester", Project: "railyard"},
		DB:  gdb,
	})
	return newHostService(h, pluginName), mock, captured
}

// assertKeyColumnQuoted fails if any captured statement references the
// reserved word "key" without backtick-quoting it. KEY is reserved in
// MySQL, so an unquoted `key` column is a parse error in production. The
// kv table is named plugin_kvs and no other token contains "key", so every
// occurrence of "key" must be wrapped as `key`.
func assertKeyColumnQuoted(t *testing.T, captured []string) {
	t.Helper()
	if len(captured) == 0 {
		t.Fatal("no SQL captured — the handler issued no query")
	}
	for _, sql := range captured {
		bare := strings.Count(sql, "key")
		quoted := strings.Count(sql, "`key`")
		if bare != quoted {
			t.Fatalf("unquoted reserved word `key` in MySQL SQL (bare=%d quoted=%d):\n  %s",
				bare, quoted, sql)
		}
	}
}

func TestKV_MySQLQuotesKeyColumn(t *testing.T) {
	ctx := context.Background()

	t.Run("Get", func(t *testing.T) {
		s, mock, captured := newMySQLCaptureHostService(t, "alpha")
		mock.ExpectQuery(".").
			WillReturnRows(sqlmock.NewRows([]string{"plugin", "key", "value", "updated_at"}))
		if _, err := s.KVGet(ctx, &protov1.KVGetRequest{Key: "cursor"}); err != nil {
			t.Fatalf("KVGet: %v", err)
		}
		assertKeyColumnQuoted(t, *captured)
	})

	t.Run("Delete", func(t *testing.T) {
		s, mock, captured := newMySQLCaptureHostService(t, "alpha")
		// GORM wraps Delete in a default transaction.
		mock.ExpectBegin()
		mock.ExpectExec(".").WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectCommit()
		if _, err := s.KVDelete(ctx, &protov1.KVDeleteRequest{Key: "cursor"}); err != nil {
			t.Fatalf("KVDelete: %v", err)
		}
		assertKeyColumnQuoted(t, *captured)
	})

	t.Run("List", func(t *testing.T) {
		s, mock, captured := newMySQLCaptureHostService(t, "alpha")
		mock.ExpectQuery(".").WillReturnRows(sqlmock.NewRows([]string{"key"}))
		if _, err := s.KVList(ctx, &protov1.KVListRequest{Prefix: "seen:"}); err != nil {
			t.Fatalf("KVList: %v", err)
		}
		assertKeyColumnQuoted(t, *captured)
	})

	t.Run("Put", func(t *testing.T) {
		s, mock, captured := newMySQLCaptureHostService(t, "alpha")
		mock.ExpectBegin()
		// count existing (for this key) → 0, so the new-key path runs
		mock.ExpectQuery(".").WillReturnRows(sqlmock.NewRows([]string{"c"}).AddRow(0))
		// count total keys → 0, under the cap
		mock.ExpectQuery(".").WillReturnRows(sqlmock.NewRows([]string{"c"}).AddRow(0))
		// upsert
		mock.ExpectExec(".").WillReturnResult(sqlmock.NewResult(1, 1))
		mock.ExpectCommit()
		if _, err := s.KVPut(ctx, &protov1.KVPutRequest{Key: "cursor", Value: []byte("42")}); err != nil {
			t.Fatalf("KVPut: %v", err)
		}
		assertKeyColumnQuoted(t, *captured)
	})
}

// TestKV_MaxValueFitsColumnType guards the off-by-one between KVMaxValueBytes
// and the PluginKV.Value column type: a value that passes the size check must
// also fit the underlying MySQL column, or a max-size Put passes validation
// then fails the insert. type:blob caps at 65535 bytes (2^16-1); mediumblob
// at 16 MiB.
func TestKV_MaxValueFitsColumnType(t *testing.T) {
	f, ok := reflect.TypeOf(models.PluginKV{}).FieldByName("Value")
	if !ok {
		t.Fatal("PluginKV has no Value field")
	}
	tag := strings.ToLower(f.Tag.Get("gorm"))

	var colMax int
	switch {
	case strings.Contains(tag, "longblob"):
		colMax = 1<<32 - 1
	case strings.Contains(tag, "mediumblob"):
		colMax = 1<<24 - 1
	case strings.Contains(tag, "blob"):
		colMax = 1<<16 - 1
	default:
		t.Fatalf("PluginKV.Value gorm tag %q has no recognized blob type", tag)
	}

	if KVMaxValueBytes > colMax {
		t.Fatalf("KVMaxValueBytes=%d exceeds the column capacity %d implied by gorm tag %q",
			KVMaxValueBytes, colMax, tag)
	}
}
