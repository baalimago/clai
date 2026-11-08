package profile_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/baalimago/clai/internal/profile"
	"github.com/baalimago/clai/internal/testenv"
	"github.com/baalimago/clai/internal/text"
)

const draft202012 = "https://json-schema.org/draft/2020-12/schema"

func TestMain(m *testing.M) { testenv.Main(m) }

// document is the parsed schema document. Its methods compare the document
// against Go types, so a member either side lacks fails the test by name.
type document map[string]any

func TestSchemaPropertiesMatchProfile(t *testing.T) {
	d := parseDocument(t)
	d.checkStruct(t, d, reflect.TypeFor[text.Profile](), "profile", "$schema", "description")
	if got, want := slices.Sorted(maps.Keys(asObject(t, d["$defs"], "$defs"))), []string{"audio", "server"}; !slices.Equal(got, want) {
		t.Fatalf("$defs = %v, want %v", got, want)
	}
}

func TestSchemaVersionMatchesID(t *testing.T) {
	if profile.SchemaVersion() != profile.Version {
		t.Fatalf("SchemaVersion() = %q, Version = %q", profile.SchemaVersion(), profile.Version)
	}
	id, ok := parseDocument(t)["$id"].(string)
	want := "https://clai.chat/schema/profile-" + profile.Version + ".json"
	if !ok || id != want {
		t.Fatalf("$id = %v, want %q for Version %q", id, want, profile.Version)
	}
}

func TestSchemaIsEmbeddedBytes(t *testing.T) {
	onDisk, err := os.ReadFile("schema.json")
	if err != nil {
		t.Fatalf("read schema.json: %v", err)
	}
	embedded := profile.Schema()
	if len(embedded) == 0 {
		t.Fatal("Schema() is empty")
	}
	if got, want := sha256.Sum256(embedded), sha256.Sum256(onDisk); got != want {
		t.Fatalf("Schema() sha256 = %x, schema.json = %x", got, want)
	}
}

func TestUnreadKeys(t *testing.T) {
	props := asObject(t, parseDocument(t)["properties"], "properties")
	var unread []string
	for name, raw := range props {
		node := asObject(t, raw, "properties/"+name)
		mark, marked := node["x-clai"]
		if !marked {
			continue
		}
		if mark != "unread" {
			t.Fatalf("properties/%s: x-clai = %v, want unread", name, mark)
		}
		unread = append(unread, name)
	}
	slices.Sort(unread)
	if want := []string{"$schema", "description"}; !slices.Equal(unread, want) {
		t.Fatalf("unread members = %v, want %v", unread, want)
	}
	read := goJSONKeys(reflect.TypeFor[text.Profile]())
	for _, name := range unread {
		if slices.Contains(read, name) {
			t.Fatalf("%s is marked unread but text.Profile reads it", name)
		}
	}
}

func TestSchemaParses(t *testing.T) {
	dec := json.NewDecoder(bytes.NewReader(profile.Schema()))
	var doc document
	if err := dec.Decode(&doc); err != nil {
		t.Fatalf("embedded document is not JSON: %v", err)
	}
	var more any
	if err := dec.Decode(&more); err != io.EOF {
		t.Fatalf("embedded document holds more than one JSON value: %v", err)
	}
	if doc["$schema"] != draft202012 {
		t.Fatalf("$schema = %v, want %s", doc["$schema"], draft202012)
	}
	refs := 0
	walkObjects(doc, "", func(path string, node map[string]any) {
		if _, ok := node["$ref"]; !ok {
			return
		}
		refs++
		doc.resolve(t, node, path)
	})
	if refs == 0 {
		t.Fatal("document holds no $ref: the walk proves nothing")
	}
}

func TestSchemaPermitsUnknown(t *testing.T) {
	levels := 0
	walkObjects(parseDocument(t), "", func(path string, node map[string]any) {
		if node["type"] != "object" {
			return
		}
		levels++
		additional, ok := node["additionalProperties"]
		switch {
		case !ok:
			t.Fatalf("%s: object level admits no member it does not name", path)
		case additional == false:
			t.Fatalf("%s: additionalProperties is false", path)
		}
		if _, names := node["properties"]; names && additional != true {
			t.Fatalf("%s: named members refuse unknown ones, additionalProperties = %v", path, additional)
		}
	})
	if levels != 7 {
		t.Fatalf("object levels = %d, want 7 (profile, mcp_servers, server, auth, audio, source, env)", levels)
	}
}

func parseDocument(t *testing.T) document {
	t.Helper()
	var doc document
	if err := json.Unmarshal(profile.Schema(), &doc); err != nil {
		t.Fatalf("embedded document is not JSON: %v", err)
	}
	if doc == nil {
		t.Fatal("embedded document is not one JSON object")
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

func goJSONKeys(typ reflect.Type) []string {
	var keys []string
	for field := range typ.Fields() {
		keys = append(keys, jsonName(field))
	}
	return keys
}

func jsonName(field reflect.StructField) string {
	name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
	return name
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
// typ's exported fields and their JSON types. extra names members the
// document may carry beyond typ's fields.
func (d document) checkStruct(t *testing.T, node map[string]any, typ reflect.Type, path string, extra ...string) {
	t.Helper()
	if node["type"] != "object" {
		t.Fatalf("%s: type = %v, want object", path, node["type"])
	}
	props := asObject(t, node["properties"], path+"/properties")
	named := map[string]bool{}
	for field := range typ.Fields() {
		name := jsonName(field)
		named[name] = true
		raw, ok := props[name]
		if !ok {
			t.Fatalf("%s: Go member %q (%s) is missing from the document", path, name, field.Type)
		}
		d.checkField(t, asObject(t, raw, path+"/properties/"+name), field.Type, path+"."+name)
	}
	for name := range props {
		if !named[name] && !slices.Contains(extra, name) {
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
