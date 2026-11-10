package configschema

import (
	"encoding/json"
	"slices"
	"strings"
)

// Kind names one synced JSON config type.
type Kind string

const (
	McpServers    Kind = "mcp-servers"
	ShellContexts Kind = "shell-contexts"
	TextConfig    Kind = "text-config"
	PhotoConfig   Kind = "photo-config"
	VideoConfig   Kind = "video-config"
	Theme         Kind = "theme"
	SkillsJSON    Kind = "skills-json"
)

// Kinds returns every kind, sorted by name.
func Kinds() []Kind {
	kinds := make([]Kind, 0, len(documentFiles))
	for kind := range documentFiles {
		kinds = append(kinds, kind)
	}
	slices.Sort(kinds)
	return kinds
}

// Document returns kind's document, byte for byte, and whether kind exists.
func Document(kind Kind) ([]byte, bool) {
	file, ok := documentFiles[kind]
	if !ok {
		return nil, false
	}
	doc, err := documents.ReadFile(file)
	if err != nil {
		return nil, false
	}
	return doc, true
}

// Version returns the major.minor the document's $id carries.
func Version(kind Kind) string {
	doc, ok := Document(kind)
	if !ok {
		return ""
	}
	var header struct {
		ID string `json:"$id"`
	}
	if err := json.Unmarshal(doc, &header); err != nil {
		return ""
	}
	rest, ok := strings.CutPrefix(header.ID, schemaIDPrefix+string(kind)+"-")
	if !ok {
		return ""
	}
	version, ok := strings.CutSuffix(rest, ".json")
	if !ok {
		return ""
	}
	return version
}
