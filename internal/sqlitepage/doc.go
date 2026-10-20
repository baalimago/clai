// Package sqlitepage reads rowid tables from a SQLite database file without
// the SQLite engine or cgo.
//
// It supports what clai needs to import another tool's local history: the
// file header, the table b-tree, records, and overflow chains. It deliberately
// refuses anything it cannot read correctly: a non-empty -wal sidecar, a
// WITHOUT ROWID table, or a page type it does not understand. A refusal is a
// typed error, never a partial or stale result.
//
// The reader is read-only. It never writes to the database and never creates
// side files.
package sqlitepage
