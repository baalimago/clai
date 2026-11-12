package configschema

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/baalimago/clai/internal/diag"
)

// Severity is how a finding is treated.
type Severity uint8

const (
	Error Severity = iota
	Warning
)

// Finding is one problem with a body.
type Finding struct {
	Member   string // JSON path, "$" for the whole body
	Reason   string // one printable sentence
	Severity Severity
}

// Check reads body as kind's document allows and returns every finding,
// sorted by Member then Reason. A body that is no JSON value returns one
// Error at "$".
func Check(kind Kind, body []byte) ([]Finding, error) {
	raw, known := Document(kind)
	if !known {
		return nil, fmt.Errorf("unknown config kind %q: the kinds are %v", kind, Kinds())
	}
	if len(body) > maxConfigBytes {
		return []Finding{{
			Member:   "$",
			Reason:   fmt.Sprintf("larger than %d KiB, the store's config file cap", maxConfigBytes/1024),
			Severity: Error,
		}}, nil
	}
	var doc vocabulary
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, diag.Errorf("the %s document does not decode: %w", kind, err)
	}
	value, findings := parse(body)
	if value == nil {
		return findings, nil
	}
	check := checker{doc: &doc}
	if err := check.value(&doc, "$", value); err != nil {
		return nil, diag.Errorf("apply the %s document: %w", kind, err)
	}
	findings = append(findings, check.out...)
	slices.SortStableFunc(findings, func(a, b Finding) int {
		if order := strings.Compare(a.Member, b.Member); order != 0 {
			return order
		}
		return strings.Compare(a.Reason, b.Reason)
	})
	return findings, nil
}

// maxConfigBytes caps a config file the store holds, so that Check refuses a
// larger body before it parses one byte of it.
const maxConfigBytes = 1 << 20

// unreadMark is the x-clai annotation on a member the decoders clai reads a
// config with do not bind: the store keeps it, so the checker warns.
const unreadMark = "unread"

// The reasons the checker answers with. Each one names one thing a body can
// get wrong, and each one is fixed text, because the corpus and the Worker's
// evaluator compare findings byte for byte.
const (
	emptyBodyReason    = "not one JSON value: the body is empty"
	severalValueReason = "not one JSON value: the body holds more than one JSON value"
	noValueReason      = "not one JSON value"
	invalidUTF8Reason  = "is not valid UTF-8"
	duplicateReason    = "appears more than once: a JSON object holds one value per member"
	unreadReason       = "clai never reads this member: the document marks it x-clai: unread"
)

// vocabulary is the document vocabulary Check implements: type, properties,
// additionalProperties, items, $ref into $defs, enum, and the x-clai
// annotation. A document that uses more fails phase 1's TestDocumentsParse, so
// the two cannot drift.
type vocabulary struct {
	Type                 string                 `json:"type"`
	Properties           map[string]*vocabulary `json:"properties"`
	AdditionalProperties jsontext.Value         `json:"additionalProperties"`
	Items                *vocabulary            `json:"items"`
	Ref                  string                 `json:"$ref"`
	Enum                 []string               `json:"enum"`
	Defs                 map[string]*vocabulary `json:"$defs"`
	XClai                string                 `json:"x-clai"`
}

// resolve follows a $ref to the definition it names.
func (n *vocabulary) resolve(node *vocabulary) (*vocabulary, error) {
	if node.Ref == "" {
		return node, nil
	}
	name, ok := strings.CutPrefix(node.Ref, "#/$defs/")
	if !ok {
		return nil, fmt.Errorf("the document's $ref %q is not under #/$defs", node.Ref)
	}
	def, ok := n.Defs[name]
	if !ok {
		return nil, fmt.Errorf("the document's $ref %q names no definition", node.Ref)
	}
	return def, nil
}

// additionalSchema is the schema an unnamed member must satisfy, or nil when
// the document leaves unnamed members to this checker: a name the document
// does not write is then a finding of its own, near miss or nothing.
func (n *vocabulary) additionalSchema() (*vocabulary, error) {
	raw := bytes.TrimSpace(n.AdditionalProperties)
	switch string(raw) {
	case "", "false", "true":
		return nil, nil
	}
	var child vocabulary
	if err := json.Unmarshal(raw, &child); err != nil {
		return nil, diag.Errorf("the document's additionalProperties does not decode: %w", err)
	}
	return &child, nil
}

// accepts reports whether a value can satisfy the node's type.
func (n *vocabulary) accepts(value *jsonValue) bool {
	switch n.Type {
	case "object":
		return value.kind == jsontext.KindBeginObject
	case "array":
		return value.kind == jsontext.KindBeginArray
	case "string":
		return value.kind == jsontext.KindString
	case "boolean":
		return value.kind == jsontext.KindTrue || value.kind == jsontext.KindFalse
	case "integer":
		return value.kind == jsontext.KindNumber && !bytes.ContainsAny(value.raw, ".eE")
	case "number":
		return value.kind == jsontext.KindNumber
	}
	return true
}

func (n *vocabulary) typeReason() string {
	switch n.Type {
	case "object":
		return "needs an object"
	case "array":
		return "needs an array"
	case "string":
		return "needs a string"
	case "boolean":
		return "needs true or false"
	case "integer":
		return "needs an integer"
	case "number":
		return "needs a number"
	}
	return "needs " + n.Type
}

// jsonValue is one value of a body: its raw text, and, for an object or an
// array, its children in the order the body writes them.
type jsonValue struct {
	kind    jsontext.Kind
	raw     jsontext.Value
	members []jsonMember
	items   []*jsonValue
	// reported marks a value the parse already spoke for, such as one that is
	// no UTF-8: the document has nothing to add to that.
	reported bool
}

// jsonMember is one member of an object, in the order the body writes it.
type jsonMember struct {
	name  string
	value *jsonValue
}

// parse reads body as one JSON value. A body that is no JSON value returns no
// value and the one finding saying so; a body that parses returns its tree and
// the findings the parse itself proves: a repeated member name and a value
// that is no UTF-8.
func parse(body []byte) (*jsonValue, []Finding) {
	if len(bytes.TrimSpace(body)) == 0 {
		return nil, []Finding{{Member: "$", Reason: emptyBodyReason, Severity: Error}}
	}
	dec := decoder(body)
	raw, err := dec.ReadValue()
	if err != nil {
		return nil, []Finding{{Member: "$", Reason: noValueReason, Severity: Error}}
	}
	// The decoder owns the bytes it hands out until its next call: keep a copy
	// of the value before asking it for what follows.
	raw = bytes.Clone(raw)
	switch _, err := dec.ReadToken(); {
	case errors.Is(err, io.EOF):
	case err != nil:
		return nil, []Finding{{Member: "$", Reason: noValueReason, Severity: Error}}
	default:
		return nil, []Finding{{Member: "$", Reason: severalValueReason, Severity: Error}}
	}
	value, findings, err := parseValue(raw, "$")
	if err != nil {
		return nil, []Finding{{Member: "$", Reason: noValueReason, Severity: Error}}
	}
	if !utf8.Valid(body) && !slices.ContainsFunc(findings, func(f Finding) bool {
		return f.Reason == invalidUTF8Reason
	}) {
		// The invalid bytes sit in a member name, which the walk reads as the
		// replacement character and cannot name.
		findings = append(findings, Finding{Member: "$", Reason: invalidUTF8Reason, Severity: Error})
	}
	return value, findings
}

// decoder reads a body with the two rules this checker enforces itself, so
// that it can name the member each one breaks at.
func decoder(raw []byte) *jsontext.Decoder {
	return jsontext.NewDecoder(bytes.NewReader(raw), jsontext.AllowDuplicateNames(true), jsontext.AllowInvalidUTF8(true))
}

func parseValue(raw jsontext.Value, path string) (*jsonValue, []Finding, error) {
	dec := decoder(raw)
	switch dec.PeekKind() {
	case jsontext.KindBeginObject:
		return parseObject(dec, path)
	case jsontext.KindBeginArray:
		return parseArray(dec, path)
	}
	value, err := dec.ReadValue()
	if err != nil {
		return nil, nil, err
	}
	return &jsonValue{kind: value.Kind(), raw: value}, nil, nil
}

func parseObject(dec *jsontext.Decoder, path string) (*jsonValue, []Finding, error) {
	if _, err := dec.ReadToken(); err != nil {
		return nil, nil, err
	}
	value := &jsonValue{kind: jsontext.KindBeginObject}
	var findings []Finding
	seen := map[string]bool{}
	for dec.PeekKind() != jsontext.KindEndObject {
		token, err := dec.ReadToken()
		if err != nil {
			return nil, nil, err
		}
		name := token.String()
		memberPath := path + "." + name
		if path == "$" {
			memberPath = name
		}
		raw, err := dec.ReadValue()
		if err != nil {
			return nil, nil, err
		}
		raw = bytes.Clone(raw)
		if seen[name] {
			findings = append(findings, Finding{Member: memberPath, Reason: duplicateReason, Severity: Error})
			continue
		}
		seen[name] = true
		child, childFindings, err := parseChild(raw, memberPath)
		if err != nil {
			return nil, nil, err
		}
		findings = append(findings, childFindings...)
		value.members = append(value.members, jsonMember{name: name, value: child})
	}
	if _, err := dec.ReadToken(); err != nil {
		return nil, nil, err
	}
	return value, findings, nil
}

func parseArray(dec *jsontext.Decoder, path string) (*jsonValue, []Finding, error) {
	if _, err := dec.ReadToken(); err != nil {
		return nil, nil, err
	}
	value := &jsonValue{kind: jsontext.KindBeginArray}
	var findings []Finding
	for dec.PeekKind() != jsontext.KindEndArray {
		raw, err := dec.ReadValue()
		if err != nil {
			return nil, nil, err
		}
		raw = bytes.Clone(raw)
		itemPath := fmt.Sprintf("%s[%d]", path, len(value.items))
		item, itemFindings, err := parseChild(raw, itemPath)
		if err != nil {
			return nil, nil, err
		}
		findings = append(findings, itemFindings...)
		value.items = append(value.items, item)
	}
	if _, err := dec.ReadToken(); err != nil {
		return nil, nil, err
	}
	return value, findings, nil
}

// parseChild reads one member's value, or one item of an array, and refuses a
// value whose bytes are no UTF-8 at the path it was found at.
func parseChild(raw jsontext.Value, path string) (*jsonValue, []Finding, error) {
	if !utf8.Valid(raw) {
		value := &jsonValue{kind: raw.Kind(), raw: raw, reported: true}
		return value, []Finding{{Member: path, Reason: invalidUTF8Reason, Severity: Error}}, nil
	}
	return parseValue(raw, path)
}

// checker applies one document to one parsed body.
type checker struct {
	doc *vocabulary
	out []Finding
}

func (c *checker) value(node *vocabulary, path string, value *jsonValue) error {
	if value.reported {
		return nil
	}
	resolved, err := c.doc.resolve(node)
	if err != nil {
		return err
	}
	if !resolved.accepts(value) {
		c.out = append(c.out, Finding{Member: path, Reason: resolved.typeReason(), Severity: Error})
		return nil
	}
	switch resolved.Type {
	case "object":
		return c.object(resolved, path, value.members)
	case "array":
		if resolved.Items == nil {
			return nil
		}
		for i, item := range value.items {
			if err := c.value(resolved.Items, fmt.Sprintf("%s[%d]", path, i), item); err != nil {
				return err
			}
		}
	case "string":
		return c.enum(resolved, path, value.raw)
	}
	return nil
}

func (c *checker) object(node *vocabulary, path string, members []jsonMember) error {
	additional, err := node.additionalSchema()
	if err != nil {
		return err
	}
	for _, member := range members {
		memberPath := path + "." + member.name
		if path == "$" {
			memberPath = member.name
		}
		if named, ok := node.Properties[member.name]; ok {
			resolved, err := c.doc.resolve(named)
			if err != nil {
				return err
			}
			if resolved.XClai == unreadMark {
				c.out = append(c.out, Finding{Member: memberPath, Reason: unreadReason, Severity: Warning})
				continue
			}
			if err := c.value(named, memberPath, member.value); err != nil {
				return err
			}
			continue
		}
		if additional != nil {
			if err := c.value(additional, memberPath, member.value); err != nil {
				return err
			}
			continue
		}
		if len(node.Properties) == 0 {
			// This level names no member, so a name here is a slot (a server
			// name, an env key) and no misspelling of anything.
			continue
		}
		if finding, ok := unknownMember(member.name, memberPath, node.Properties); ok {
			c.out = append(c.out, finding)
		}
	}
	return nil
}

func (c *checker) enum(node *vocabulary, path string, raw jsontext.Value) error {
	if len(node.Enum) == 0 {
		return nil
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return diag.Errorf("the %s word at %s does not decode: %w", node.Type, path, err)
	}
	if slices.Contains(node.Enum, value) {
		return nil
	}
	allowed, err := json.Marshal(node.Enum)
	if err != nil {
		return diag.Errorf("the document's enum at %s does not encode: %w", path, err)
	}
	c.out = append(c.out, Finding{Member: path, Reason: fmt.Sprintf("needs one of %s", allowed), Severity: Error})
	return nil
}

// unknownMember is the finding for a name the document does not write at its
// level, when the name is a near miss of one it does: the documented name with
// another case or separator, or within two edits of it. A name that is no near
// miss is no finding at all.
func unknownMember(name, path string, documented map[string]*vocabulary) (Finding, bool) {
	names := slices.Sorted(maps.Keys(documented))
	fixed, ok := caseVariant(name, names)
	if !ok {
		fixed = nearest(name, names)
	}
	if fixed == "" {
		return Finding{}, false
	}
	return Finding{
		Member:   path,
		Reason:   fmt.Sprintf("unknown member %q: did you mean %q?", name, fixed),
		Severity: Error,
	}, true
}

// caseVariant returns the documented name that differs from name only by case,
// '-' or '_'.
func caseVariant(name string, names []string) (string, bool) {
	normal := normaliseName(name)
	for _, candidate := range names {
		if normaliseName(candidate) == normal {
			return candidate, true
		}
	}
	return "", false
}

func normaliseName(name string) string {
	return strings.NewReplacer("-", "", "_", "").Replace(strings.ToLower(name))
}

// nearest returns the documented name within two edits of name, when the two
// are close enough that name can be a misspelling and not a short word that
// happens to sit near a short member.
func nearest(name string, names []string) string {
	lowered := strings.ToLower(name)
	best, bestDistance := "", 3
	for _, candidate := range names {
		loweredCandidate := strings.ToLower(candidate)
		distance := editDistance(lowered, loweredCandidate)
		closeEnough := distance == 1 || sharedPrefix(lowered, loweredCandidate) >= 2
		if distance <= 2 && closeEnough && distance < bestDistance {
			best, bestDistance = candidate, distance
		}
	}
	return best
}

func sharedPrefix(a, b string) int {
	n := min(len(a), len(b))
	for i := range n {
		if a[i] != b[i] {
			return i
		}
	}
	return n
}

func editDistance(a, b string) int {
	previous := make([]int, len(b)+1)
	current := make([]int, len(b)+1)
	for j := range previous {
		previous[j] = j
	}
	for i := 1; i <= len(a); i++ {
		current[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			current[j] = min(previous[j]+1, current[j-1]+1, previous[j-1]+cost)
		}
		previous, current = current, previous
	}
	return previous[len(b)]
}
