package configschema_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/baalimago/clai/internal/configschema"
	"github.com/baalimago/clai/internal/photo"
	"github.com/baalimago/clai/internal/skills"
	"github.com/baalimago/clai/internal/testenv"
	"github.com/baalimago/clai/internal/text"
	"github.com/baalimago/clai/internal/utils"
	"github.com/baalimago/clai/internal/video"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

const (
	draft202012  = "https://json-schema.org/draft/2020-12/schema"
	schemaPrefix = "https://clai.chat/schema/"
)

func TestMain(m *testing.M) { testenv.Main(m) }

// unreadMark is the annotation a document puts on a member clai's decoder
// does not bind.
const unreadMark = "unread"

// goType names the Go type whose JSON tags each kind's document must match:
// the type the loader unmarshals that kind's file into.
var goType = map[configschema.Kind]reflect.Type{
	configschema.McpServers:    reflect.TypeFor[pub_models.McpServer](),
	configschema.ShellContexts: reflect.TypeFor[text.ShellContextDefinition](),
	configschema.TextConfig:    reflect.TypeFor[text.Configurations](),
	configschema.PhotoConfig:   reflect.TypeFor[photo.Configurations](),
	configschema.VideoConfig:   reflect.TypeFor[video.Configurations](),
	configschema.Theme:         reflect.TypeFor[utils.Theme](),
	configschema.SkillsJSON:    reflect.TypeFor[skills.Config](),
}

// document is one parsed schema document. Its methods compare the document
// against a Go type, so a member either side lacks fails the test by name.
type document map[string]any

// Proves clause 1: every JSON member the kind's Go type reads appears in its
// document with the type the decoder binds, and a member the type does not
// read is marked x-clai: unread.
func TestDocumentsMatchGoTypes(t *testing.T) {
	want := slices.Sorted(maps.Keys(goType))
	if got := configschema.Kinds(); !slices.Equal(got, want) {
		t.Fatalf("Kinds() = %v, want %v", got, want)
	}
	for _, kind := range configschema.Kinds() {
		doc := parse(t, kind)
		doc.checkStruct(t, doc, goType[kind], string(kind))
	}
}

// Proves clause 2: Version is the major.minor in the document's $id, and the
// $id carries nothing else.
func TestVersionMatchesID(t *testing.T) {
	for _, kind := range configschema.Kinds() {
		version := configschema.Version(kind)
		if version == "" {
			t.Fatalf("%s: Version() = %q", kind, version)
		}
		id, ok := parse(t, kind)["$id"].(string)
		if !ok {
			t.Fatalf("%s: $id = %v, want a string", kind, parse(t, kind)["$id"])
		}
		if !strings.HasSuffix(id, "-"+version+".json") {
			t.Fatalf("%s: $id %q does not end in Version() %q", kind, id, version)
		}
		if want := schemaPrefix + string(kind) + "-" + version + ".json"; id != want {
			t.Fatalf("%s: $id = %q, want %q", kind, id, want)
		}
	}
}

// Proves clause 3: Document returns the embedded bytes unchanged, never a
// re-encoded form.
func TestDocumentsAreEmbeddedBytes(t *testing.T) {
	for _, kind := range configschema.Kinds() {
		embedded, ok := configschema.Document(kind)
		if !ok {
			t.Fatalf("%s: Document() reports no document", kind)
		}
		onDisk, err := os.ReadFile(filepath.Join("schema", string(kind)+".json"))
		if err != nil {
			t.Fatalf("%s: read schema file: %v", kind, err)
		}
		if got, want := sha256.Sum256(embedded), sha256.Sum256(onDisk); got != want {
			t.Fatalf("%s: Document() sha256 = %x, schema/%s.json = %x", kind, got, kind, want)
		}
	}
}

// Proves clause 5: a kind that is not a constant answers nil, false and never
// panics.
func TestUnknownKind(t *testing.T) {
	for _, kind := range []configschema.Kind{"nope", "", "MCP-SERVERS", "skill"} {
		doc, ok := configschema.Document(kind)
		if ok || doc != nil {
			t.Fatalf("Document(%q) = %v, %v, want nil, false", kind, doc, ok)
		}
		if version := configschema.Version(kind); version != "" {
			t.Fatalf("Version(%q) = %q, want empty", kind, version)
		}
	}
}

// Proves clause 4: the documents describe and permit unknown members at every
// level; the checker refuses them, never the document.
func TestDocumentsPermitUnknown(t *testing.T) {
	for _, kind := range configschema.Kinds() {
		levels := 0
		walkObjects(parse(t, kind), "", func(path string, node map[string]any) {
			if node["type"] != "object" {
				return
			}
			levels++
			additional, ok := node["additionalProperties"]
			switch {
			case !ok:
				t.Fatalf("%s%s: object level admits no member it does not name", kind, path)
			case additional == false:
				t.Fatalf("%s%s: additionalProperties is false", kind, path)
			}
			if _, names := node["properties"]; names && additional != true {
				t.Fatalf("%s%s: named members refuse unknown ones, additionalProperties = %v", kind, path, additional)
			}
		})
		if levels == 0 {
			t.Fatalf("%s: the document holds no object level", kind)
		}
	}
}

// Proves clause 1: every document parses as one JSON object and every $ref
// resolves under $defs, with no network access.
func TestDocumentsParse(t *testing.T) {
	refs := 0
	for _, kind := range configschema.Kinds() {
		raw, ok := configschema.Document(kind)
		if !ok {
			t.Fatalf("%s: Document() reports no document", kind)
		}
		dec := json.NewDecoder(bytes.NewReader(raw))
		var doc document
		if err := dec.Decode(&doc); err != nil {
			t.Fatalf("%s: document is not JSON: %v", kind, err)
		}
		var more any
		if err := dec.Decode(&more); err != io.EOF {
			t.Fatalf("%s: document holds more than one JSON value: %v", kind, err)
		}
		if doc["$schema"] != draft202012 {
			t.Fatalf("%s: $schema = %v, want %s", kind, doc["$schema"], draft202012)
		}
		walkObjects(doc, "", func(path string, node map[string]any) {
			if _, ok := node["$ref"]; !ok {
				return
			}
			refs++
			doc.resolve(t, node, string(kind)+path)
		})
	}
	if refs == 0 {
		t.Fatal("no document holds a $ref: the walk proves nothing")
	}
}

// Proves clause 6: Document answers the bytes the binary was built with while
// no checkout is on disk.
func TestDocumentsReadNoFile(t *testing.T) {
	built := map[configschema.Kind][]byte{}
	for _, kind := range configschema.Kinds() {
		doc, ok := configschema.Document(kind)
		if !ok {
			t.Fatalf("%s: Document() reports no document", kind)
		}
		built[kind] = slices.Clone(doc)
	}
	t.Chdir(t.TempDir())
	for kind, want := range built {
		if _, err := os.Stat(filepath.Join("schema", string(kind)+".json")); !os.IsNotExist(err) {
			t.Fatalf("%s: a source tree is still on disk, the test proves nothing: %v", kind, err)
		}
		got, ok := configschema.Document(kind)
		if !ok {
			t.Fatalf("%s: Document() reports no document without a checkout", kind)
		}
		if len(got) == 0 {
			t.Fatalf("%s: Document() is empty without a checkout", kind)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("%s: Document() without a checkout differs from the built bytes", kind)
		}
	}
}

func parse(t *testing.T, kind configschema.Kind) document {
	t.Helper()
	raw, ok := configschema.Document(kind)
	if !ok {
		t.Fatalf("%s: Document() reports no document", kind)
	}
	var doc document
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("%s: document is not JSON: %v", kind, err)
	}
	if doc == nil {
		t.Fatalf("%s: document is not one JSON object", kind)
	}
	return doc
}

func asObject(t *testing.T, raw any, path string) map[string]any {
	t.Helper()
	node, ok := raw.(map[string]any)
	if !ok {
		t.Fatalf("%s: %T, want one JSON object", path, raw)
	}
	return node
}

func walkObjects(node map[string]any, path string, visit func(path string, node map[string]any)) {
	visit(path, node)
	for name, raw := range node {
		switch value := raw.(type) {
		case map[string]any:
			walkObjects(value, path+"/"+name, visit)
		case []any:
			for i, item := range value {
				if child, ok := item.(map[string]any); ok {
					walkObjects(child, fmt.Sprintf("%s/%s/%d", path, name, i), visit)
				}
			}
		}
	}
}

func (d document) resolve(t *testing.T, node map[string]any, path string) map[string]any {
	t.Helper()
	ref, ok := node["$ref"].(string)
	if !ok {
		return node
	}
	name, ok := strings.CutPrefix(ref, "#/$defs/")
	if !ok || name == "" {
		t.Fatalf("%s: $ref %q is not under #/$defs", path, ref)
	}
	return asObject(t, asObject(t, d["$defs"], "$defs")[name], "#/$defs/"+name)
}

// checkStruct compares one object node's named members and their types with
// typ's exported fields and their JSON names. A member the document names and
// the type does not read must be marked x-clai: unread.
func (d document) checkStruct(t *testing.T, node map[string]any, typ reflect.Type, path string) {
	t.Helper()
	if node["type"] != "object" {
		t.Fatalf("%s: type = %v, want object for %s", path, node["type"], typ)
	}
	props := asObject(t, node["properties"], path+"/properties")
	read := map[string]bool{}
	for field := range typ.Fields() {
		name, ok := jsonName(field)
		if !ok {
			continue
		}
		read[name] = true
		raw, ok := props[name]
		if !ok {
			t.Fatalf("%s: Go member %q (%s) is missing from the document", path, name, field.Type)
		}
		d.checkField(t, asObject(t, raw, path+"/properties/"+name), field.Type, path+"."+name)
	}
	for name, raw := range props {
		if read[name] {
			continue
		}
		if mark := asObject(t, raw, path+"/properties/"+name)["x-clai"]; mark != unreadMark {
			t.Fatalf("%s: document member %q is read by no Go code and is not marked x-clai: unread", path, name)
		}
	}
}

func (d document) checkField(t *testing.T, node map[string]any, typ reflect.Type, path string) {
	t.Helper()
	if typ.Kind() == reflect.Pointer {
		d.checkField(t, node, typ.Elem(), path)
		return
	}
	want := "object"
	switch typ.Kind() {
	case reflect.String:
		want = "string"
		d.checkEnum(t, node, typ, path)
	case reflect.Bool:
		want = "boolean"
	case reflect.Int:
		want = "integer"
	case reflect.Slice:
		d.checkArray(t, node, typ, path)
		return
	case reflect.Map:
		d.checkMap(t, node, typ, path)
		return
	case reflect.Struct:
		d.checkStruct(t, d.resolve(t, node, path), typ, path)
		return
	default:
		t.Fatalf("%s: no schema mapping for Go type %s", path, typ)
	}
	if node["type"] != want {
		t.Fatalf("%s: type = %v, want %s for %s", path, node["type"], want, typ)
	}
}

func (d document) checkArray(t *testing.T, node map[string]any, typ reflect.Type, path string) {
	t.Helper()
	if typ.Elem().Kind() != reflect.String {
		t.Fatalf("%s: no schema mapping for Go type %s", path, typ)
	}
	if node["type"] != "array" {
		t.Fatalf("%s: type = %v, want array for %s", path, node["type"], typ)
	}
	if items := asObject(t, node["items"], path+"/items"); items["type"] != "string" {
		t.Fatalf("%s/items: type = %v, want string for %s", path, items["type"], typ)
	}
}

func (d document) checkMap(t *testing.T, node map[string]any, typ reflect.Type, path string) {
	t.Helper()
	if node["type"] != "object" {
		t.Fatalf("%s: type = %v, want object for %s", path, node["type"], typ)
	}
	values := asObject(t, node["additionalProperties"], path+"/additionalProperties")
	d.checkField(t, values, typ.Elem(), path+"[]")
}

func (d document) checkEnum(t *testing.T, node map[string]any, typ reflect.Type, path string) {
	t.Helper()
	raw, ok := node["enum"]
	if !ok {
		return
	}
	values, ok := raw.([]any)
	if !ok || len(values) == 0 {
		t.Fatalf("%s: enum = %v, want a non-empty array", path, raw)
	}
	for _, value := range values {
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatalf("%s: enum value %v: %v", path, value, err)
		}
		if err := json.Unmarshal(encoded, reflect.New(typ).Interface()); err != nil {
			t.Fatalf("%s: the document admits %s and the Go decoder refuses it: %v", path, encoded, err)
		}
	}
}

// jsonName returns the JSON member encoding/json binds field to, and false for
// a field it skips.
func jsonName(field reflect.StructField) (string, bool) {
	if field.PkgPath != "" {
		return "", false
	}
	name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
	switch name {
	case "-":
		return "", false
	case "":
		return field.Name, true
	}
	return name, true
}
