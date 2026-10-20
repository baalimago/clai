package sqlitepage

import "errors"

// Typed failures let a source reader decide whether a refused database is a
// normal absence or a real problem.
var (
	ErrNotSQLite = errors.New("not a sqlite database")
	// ErrWALPresent marks a `-wal` sidecar this reader cannot replay. Reading
	// without it would silently return a stale snapshot.
	ErrWALPresent = errors.New("write-ahead log present; refusing a stale read")
	ErrCorrupt    = errors.New("corrupt database")
	// ErrUnsupported marks a valid SQLite shape this reader cannot decode,
	// such as a WITHOUT ROWID table or an index b-tree.
	ErrUnsupported = errors.New("unsupported by this reader")
)
