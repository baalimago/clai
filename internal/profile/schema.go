// Package profile holds the clai profile schema document and the checker that
// applies it.
package profile

import _ "embed"

// Version is the major.minor this document declares. The release tag's
// major.minor must equal it.
const Version = "1.0"

//go:embed schema.json
var schema []byte

// Schema returns the document this binary ships, byte for byte.
func Schema() []byte { return schema }

// SchemaVersion is the version string the document's $id carries.
func SchemaVersion() string { return Version }
