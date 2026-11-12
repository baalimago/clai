// Package configschema holds the JSON Schema (draft 2020-12) documents that
// describe clai's synced config kinds, embedded so a binary ships them and
// reads no document from disk at run time.
package configschema

import "embed"

// schemaIDPrefix is the base every document's $id starts with.
const schemaIDPrefix = "https://clai.chat/schema/"

//go:embed schema/*.json
var documents embed.FS

// documentFiles maps a kind to the embedded file holding its document.
var documentFiles = map[Kind]string{
	McpServers:    "schema/mcp-servers.json",
	ShellContexts: "schema/shell-contexts.json",
	TextConfig:    "schema/text-config.json",
	PhotoConfig:   "schema/photo-config.json",
	VideoConfig:   "schema/video-config.json",
	Theme:         "schema/theme.json",
	SkillsJSON:    "schema/skills-json.json",
}
