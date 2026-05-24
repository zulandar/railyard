package db

import (
	"io"
	"log"
	"os"

	gomysql "github.com/go-sql-driver/mysql"
)

// SilenceDriverLogger swaps the go-sql-driver/mysql package-level logger to
// io.Discard and returns a function that restores the driver's default
// stderr logger. Use during readiness probe loops against a starting MySQL
// container: the docker port is forwarded before mysqld is ready, so each
// probe attempt produces an "unexpected EOF" line that confuses users.
// Errors are still returned to callers via the database/sql API — this only
// suppresses the driver's package-level Print calls.
func SilenceDriverLogger() func() {
	_ = gomysql.SetLogger(log.New(io.Discard, "", 0))
	return func() {
		_ = gomysql.SetLogger(log.New(os.Stderr, "[mysql] ", log.Ldate|log.Ltime|log.Lshortfile))
	}
}
