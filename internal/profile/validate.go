package profile

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"maps"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"

	"github.com/baalimago/clai/internal/diag"
	"github.com/baalimago/clai/internal/remote"
	"github.com/baalimago/clai/internal/tools"
	"github.com/baalimago/clai/internal/tools/mcp/serverconfig"
)

// Route is a way a profile body arrives for a check.
type Route uint8

const (
	File    Route = iota // -prp <path>, clai profiles validate <path>
	Remote               // -prp cchub: | gh: | https:
	Publish              // profiles publish, the Hub form, POST /api/v1/hub
)

// Severity is how a finding is treated by the route it names.
type Severity uint8

const (
	Error Severity = iota
	Warning
)

// Finding is one problem with a body.
type Finding struct {
	Severity Severity
	Member   string  // JSON path, "audio.model", "$" for the whole body
	Reason   string  // one printable sentence
	Routes   []Route // the routes that refuse it; empty means all three
	Fixed    string  // the member name meant, when Member is a near miss
	// Unread marks a member clai never reads, so that a caller which shows
	// "not read by clai" (remote.Summarize) can tell it from a warning about
	// a member clai does read.
	Unread bool
}

// Findings is every finding for one body, sorted by Member then Reason.
type Findings []Finding

// Env is the machine-local knowledge a check needs, passed in so that a check
// is offline and reproducible. The caller builds it once per run.
type Env struct {
	// ResolveModel returns the candidate models a model string routes to; an
	// alias expands to its candidates. An empty result means it routes nowhere.
	ResolveModel func(model string) ([]string, error)
	// HasShellContext reports whether a shell context of that name exists in
	// the caller's config dir.
	HasShellContext func(name string) bool
	// ToolNames are the tool names a run can reach on this machine, aliases
	// included. Empty means the route has no registry.
	ToolNames []string
}

// Failed reports whether the route refuses the body.
func (f Findings) Failed(route Route) bool {
	return slices.ContainsFunc(f, func(finding Finding) bool {
		return finding.Severity == Error && finding.appliesTo(route)
	})
}

// Problems returns the reasons the route refuses the body, in order, each
// after the member it names as `member: reason`.
func (f Findings) Problems(route Route) []string {
	var reasons []string
	for _, finding := range f {
		if finding.Severity == Error && finding.appliesTo(route) {
			reasons = append(reasons, finding.Member+": "+finding.Reason)
		}
	}
	return reasons
}

// appliesTo reports whether a finding names route: no route tag names all three.
func (f Finding) appliesTo(route Route) bool {
	return len(f.Routes) == 0 || slices.Contains(f.Routes, route)
}

// Validate applies the schema document and the knowledge a schema cannot
// carry. It opens no socket, starts no process and writes no file: every
// machine-local answer arrives through env. route names the way the body
// arrived; it does not narrow the result, because every finding carries the
// routes that refuse it and the caller asks Findings.Failed for its own.
//
// A JSON null is read as if the member, or the array item, were not there, at
// every object level and inside arrays: Go unmarshals null into any field
// without error, so clai reads "args": null as if args were absent. A body
// clai's own profile migration wrote therefore checks like the body it meant.
//
// An error is returned only when the document itself cannot be applied, never
// for a problem with the body: a body problem is a finding.
func Validate(body []byte, route Route, env Env) (Findings, error) {
	if len(body) > remote.MaxBodyBytes {
		return Findings{{
			Severity: Error,
			Member:   "$",
			Reason:   fmt.Sprintf("larger than %d KiB, the profile body cap", remote.MaxBodyBytes/1024),
		}}, nil
	}
	doc, err := loadDocument()
	if err != nil {
		return nil, err
	}
	members, problem := readBody(body)
	if problem != "" {
		return Findings{{Severity: Error, Member: "$", Reason: problem}}, nil
	}
	check := checker{doc: doc}
	if err := check.object(doc, "$", members); err != nil {
		return nil, err
	}
	findings := append(check.out, env.problems(doc, members)...)
	sortFindings(findings)
	return findings, nil
}

// checker applies the document to one body.
type checker struct {
	doc *schemaNode
	out Findings
}

// object applies node to the members of one JSON object.
func (c *checker) object(node *schemaNode, path string, members map[string]jsontext.Value) error {
	additional, err := node.additionalSchema()
	if err != nil {
		return err
	}
	for _, name := range slices.Sorted(maps.Keys(members)) {
		raw := members[name]
		memberPath := path + "." + name
		if path == "$" {
			memberPath = name
		}
		if child, known := node.Properties[name]; known {
			if err := c.value(child, memberPath, raw); err != nil {
				return err
			}
			continue
		}
		if additional != nil {
			if err := c.value(additional, memberPath, raw); err != nil {
				return err
			}
			continue
		}
		if len(node.Properties) == 0 {
			// This level names no member, so a name here is a slot (a server
			// name, an env key) and not a misspelling of anything.
			continue
		}
		c.out = append(c.out, unknownMember(name, memberPath, node.Properties))
	}
	return nil
}

// value applies node to one member value.
func (c *checker) value(node *schemaNode, path string, raw jsontext.Value) error {
	resolved, err := c.doc.resolve(node)
	if err != nil {
		return err
	}
	if !resolved.accepts(raw.Kind()) {
		c.out = append(c.out, Finding{Severity: Error, Member: path, Reason: resolved.typeReason()})
		return nil
	}
	switch resolved.Type {
	case "object":
		members, err := membersOf(raw)
		if err != nil {
			c.out = append(c.out, Finding{
				Severity: Error,
				Member:   path,
				Reason:   "not one JSON object: " + strings.TrimPrefix(err.Error(), "jsontext: "),
			})
			return nil
		}
		return c.object(resolved, path, members)
	case "array":
		if resolved.Items == nil {
			return nil
		}
		var items []jsontext.Value
		if err := json.Unmarshal(raw, &items); err != nil {
			return diag.Errorf("apply the profile document at %s: %w", path, err)
		}
		for i, item := range items {
			if err := c.value(resolved.Items, fmt.Sprintf("%s[%d]", path, i), item); err != nil {
				return err
			}
		}
	case "string":
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return diag.Errorf("apply the profile document at %s: %w", path, err)
		}
		if len(resolved.Enum) == 0 || slices.Contains(resolved.Enum, value) {
			return nil
		}
		allowed, err := json.Marshal(resolved.Enum)
		if err != nil {
			return diag.Errorf("the profile document's enum at %s does not encode: %w", path, err)
		}
		c.out = append(c.out, Finding{
			Severity: Error,
			Member:   path,
			Reason:   fmt.Sprintf("needs one of %s", allowed),
		})
	case "integer":
		var value int
		if err := json.Unmarshal(raw, &value); err != nil {
			// A number that is no integer, such as 1.5.
			c.out = append(c.out, Finding{Severity: Error, Member: path, Reason: resolved.typeReason()})
		}
	}
	return nil
}

// schemaNode is the vocabulary the document uses: type, properties,
// additionalProperties, items, $ref and enum.
type schemaNode struct {
	ID                   string                 `json:"$id"`
	Type                 string                 `json:"type"`
	Properties           map[string]*schemaNode `json:"properties"`
	AdditionalProperties jsontext.Value         `json:"additionalProperties"`
	Items                *schemaNode            `json:"items"`
	Ref                  string                 `json:"$ref"`
	Enum                 []string               `json:"enum"`
	Defs                 map[string]*schemaNode `json:"$defs"`
	// versionPattern is the $id with its major.minor replaced by a capture,
	// so that a body's $schema can name another version.
	versionPattern *regexp.Regexp
}

// loadDocument decodes the embedded document once per process. It cannot fail
// on a shipped binary; a failure is the one thing Validate reports as an error.
var loadDocument = sync.OnceValues(func() (*schemaNode, error) {
	var doc schemaNode
	if err := json.Unmarshal(schema, &doc); err != nil {
		return nil, diag.Errorf("the embedded profile document does not decode: %w", err)
	}
	if doc.Type != "object" || len(doc.Properties) == 0 {
		return nil, fmt.Errorf("the embedded profile document is not one object with properties")
	}
	doc.versionPattern = schemaVersionPattern(doc.ID)
	return &doc, nil
})

// schemaVersionPattern reads the served path from the document's $id so that
// a change to the path cannot silently disable the version check.
func schemaVersionPattern(id string) *regexp.Regexp {
	const marker = "profile-"
	at := strings.Index(id, marker)
	if at < 0 {
		return nil
	}
	suffix := id[at+len(marker):]
	end := strings.LastIndex(suffix, ".json")
	if end < 0 {
		return nil
	}
	prefix := regexp.QuoteMeta(id[:at+len(marker)])
	return regexp.MustCompile(prefix + `([0-9]+\.[0-9]+)` + regexp.QuoteMeta(suffix[end:]) + `$`)
}

// resolve follows a $ref to the definition it names.
func (n *schemaNode) resolve(node *schemaNode) (*schemaNode, error) {
	if node.Ref == "" {
		return node, nil
	}
	name, ok := strings.CutPrefix(node.Ref, "#/$defs/")
	if !ok {
		return nil, fmt.Errorf("the profile document's $ref %q is not under #/$defs", node.Ref)
	}
	def, ok := n.Defs[name]
	if !ok {
		return nil, fmt.Errorf("the profile document's $ref %q names no definition", node.Ref)
	}
	return def, nil
}

// additionalSchema is the schema an unnamed member must satisfy, or nil when
// the document leaves unnamed members to this checker: a member the document
// does not name is a finding of its own, near miss or warning.
func (n *schemaNode) additionalSchema() (*schemaNode, error) {
	raw := bytes.TrimSpace(n.AdditionalProperties)
	switch string(raw) {
	case "", "false", "true":
		return nil, nil
	}
	var child schemaNode
	if err := json.Unmarshal(raw, &child); err != nil {
		return nil, diag.Errorf("the profile document's additionalProperties does not decode: %w", err)
	}
	return &child, nil
}

// accepts reports whether a JSON value of kind can satisfy the node's type.
func (n *schemaNode) accepts(kind jsontext.Kind) bool {
	switch n.Type {
	case "object":
		return kind == jsontext.KindBeginObject
	case "array":
		return kind == jsontext.KindBeginArray
	case "string":
		return kind == jsontext.KindString
	case "boolean":
		return kind == jsontext.KindTrue || kind == jsontext.KindFalse
	case "integer", "number":
		return kind == jsontext.KindNumber
	}
	return true
}

func (n *schemaNode) typeReason() string {
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

// unknownMember is the finding for a name the document does not define at its
// level: an error when it is the documented name with another case or
// separator, or within two edits of one, and a warning otherwise.
func unknownMember(name, path string, documented map[string]*schemaNode) Finding {
	names := slices.Sorted(maps.Keys(documented))
	finding := Finding{
		Severity: Warning,
		Member:   path,
		Reason:   fmt.Sprintf("unknown member %q: no member of the profile document is like it", name),
		Unread:   true,
	}
	fixed, ok := caseVariant(name, names)
	if !ok {
		fixed = nearest(name, names)
	}
	if fixed == "" {
		return finding
	}
	finding.Severity = Error
	finding.Fixed = fixed
	finding.Reason = fmt.Sprintf("unknown member %q: did you mean %q?", name, fixed)
	return finding
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
// happens to sit near a short member: "note" is no misspelling of "name",
// while "ur" is one of "url".
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

// readBody reads the body as exactly one JSON object, with every JSON null
// read as an absent member or array item. It returns the one finding's reason
// when the body is anything else: malformed, empty, more than one value, a
// duplicate member name, or invalid UTF-8.
func readBody(body []byte) (map[string]jsontext.Value, string) {
	if len(bytes.TrimSpace(body)) == 0 {
		return nil, "not one JSON object: the body is empty"
	}
	if jsontext.Value(body).Kind() != jsontext.KindBeginObject {
		return nil, "not one JSON object"
	}
	var members map[string]jsontext.Value
	if err := json.Unmarshal(body, &members); err != nil {
		return nil, "not one JSON object: " + strings.TrimPrefix(err.Error(), "jsontext: ")
	}
	if members == nil {
		members = map[string]jsontext.Value{}
	}
	dropNulls(members)
	return members, ""
}

// dropNulls reads every JSON null as if the member, or the array item, were
// not there, at every object level and inside arrays. A value that does not
// decode is left as it is, for the checker to report.
func dropNulls(members map[string]jsontext.Value) bool {
	changed := false
	for name, raw := range members {
		if raw.Kind() == jsontext.KindNull {
			delete(members, name)
			changed = true
			continue
		}
		if cleaned, dropped := dropNullsIn(raw); dropped {
			members[name] = cleaned
			changed = true
		}
	}
	return changed
}

// dropNullsIn is dropNulls for one value, reporting whether it changed.
func dropNullsIn(raw jsontext.Value) (jsontext.Value, bool) {
	switch raw.Kind() {
	case jsontext.KindBeginObject:
		var members map[string]jsontext.Value
		if err := json.Unmarshal(raw, &members); err != nil {
			return raw, false
		}
		if !dropNulls(members) {
			return raw, false
		}
		cleaned, err := json.Marshal(members)
		if err != nil {
			return raw, false
		}
		return cleaned, true
	case jsontext.KindBeginArray:
		var items []jsontext.Value
		if err := json.Unmarshal(raw, &items); err != nil {
			return raw, false
		}
		kept := make([]jsontext.Value, 0, len(items))
		changed := false
		for _, item := range items {
			if item.Kind() == jsontext.KindNull {
				changed = true
				continue
			}
			if cleaned, itemChanged := dropNullsIn(item); itemChanged {
				kept = append(kept, cleaned)
				changed = true
				continue
			}
			kept = append(kept, item)
		}
		if !changed {
			return raw, false
		}
		cleaned, err := json.Marshal(kept)
		if err != nil {
			return raw, false
		}
		return cleaned, true
	}
	return raw, false
}

func membersOf(raw jsontext.Value) (map[string]jsontext.Value, error) {
	var members map[string]jsontext.Value
	if err := json.Unmarshal(raw, &members); err != nil {
		return nil, err
	}
	if members == nil {
		members = map[string]jsontext.Value{}
	}
	return members, nil
}

// problems is the knowledge a schema cannot carry, in a fixed order.
func (env Env) problems(doc *schemaNode, members map[string]jsontext.Value) Findings {
	var findings Findings
	findings = append(findings, doc.schemaVersionProblems(members)...)
	findings = append(findings, env.modelProblems(members)...)
	findings = append(findings, env.shellContextProblems(members)...)
	findings = append(findings, env.toolProblems(members)...)
	findings = append(findings, env.audioProblems(members)...)
	findings = append(findings, skillProblems(members)...)
	findings = append(findings, serverProblems(members)...)
	return findings
}

// schemaVersionProblems refuses another profile-schema version on publish and
// warns about it elsewhere, naming both versions.
func (n *schemaNode) schemaVersionProblems(members map[string]jsontext.Value) Findings {
	value, ok := stringMember(members, "$schema")
	if !ok || n.versionPattern == nil {
		return nil
	}
	match := n.versionPattern.FindStringSubmatch(value)
	if match == nil || match[1] == Version {
		return nil
	}
	reason := fmt.Sprintf("$schema names profile version %s; this clai reads %s", match[1], SchemaVersion())
	return Findings{
		{Severity: Error, Member: "$schema", Reason: reason, Routes: []Route{Publish}},
		{Severity: Warning, Member: "$schema", Reason: reason, Routes: []Route{File, Remote}},
	}
}

// modelProblems routes model through Env.ResolveModel. An alias expands to its
// candidates and each candidate must route; a candidate is not expanded again.
func (env Env) modelProblems(members map[string]jsontext.Value) Findings {
	model, ok := stringMember(members, "model")
	if !ok || model == "" || env.ResolveModel == nil {
		return nil
	}
	candidates, err := env.ResolveModel(model)
	if err != nil || len(candidates) == 0 {
		return Findings{{
			Severity: Error,
			Member:   "model",
			Reason:   fmt.Sprintf("model %q routes to no model", model),
		}}
	}
	var findings Findings
	for _, candidate := range candidates {
		if candidate == model {
			continue
		}
		if reached, err := env.ResolveModel(candidate); err != nil || len(reached) == 0 {
			findings = append(findings, Finding{
				Severity: Error,
				Member:   "model",
				Reason:   fmt.Sprintf("model %q is an alias whose candidate %q routes to no model", model, candidate),
			})
		}
	}
	return findings
}

// shellContextProblems refuses shell_context on publish, where it belongs to
// the runner, and where the caller's config dir does not hold it.
func (env Env) shellContextProblems(members map[string]jsontext.Value) Findings {
	name, ok := stringMember(members, "shell_context")
	if !ok || strings.TrimSpace(name) == "" {
		return nil
	}
	findings := Findings{{
		Severity: Error,
		Member:   "shell_context",
		Routes:   []Route{Publish},
		Reason:   "publish refuses shell_context: a shell context is the runner's own",
	}}
	if env.HasShellContext != nil && !env.HasShellContext(name) {
		findings = append(findings, Finding{
			Severity: Error,
			Member:   "shell_context",
			Routes:   []Route{File, Remote},
			Reason:   fmt.Sprintf("shell_context %q is not a shell context in your config dir", name),
		})
	}
	return findings
}

// toolProblems resolves tools against the registry the machine has, and warns
// about a tools list no run reads. An entry in the MCP namespace is not
// judged: its name exists only while a server runs, so the registry cannot
// hold it and a refusal would contradict the run, which selects such an entry
// without a lookup (architecture/profiles.md, the profile checker).
func (env Env) toolProblems(members map[string]jsontext.Value) Findings {
	list, hasTools := stringsMember(members, "tools")
	useTools, hasUseTools := boolMember(members, "use_tools")
	cmdBan, hasCmdBan := stringsMember(members, "cmd-ban")
	var findings Findings
	if hasTools && len(env.ToolNames) > 0 {
		for _, entry := range list {
			if toolReachable(entry, env.ToolNames) || tools.SelectsMCPTools(entry) {
				continue
			}
			reason := fmt.Sprintf("tools: %q is a tool this machine cannot reach", entry)
			if near := nearest(entry, env.ToolNames); near != "" {
				reason += fmt.Sprintf(" (did you mean %q?)", near)
			}
			findings = append(findings, Finding{
				Severity: Error,
				Member:   "tools",
				Routes:   []Route{File, Remote},
				Reason:   reason,
			})
		}
	}
	if hasTools && hasUseTools && !useTools && len(list) > 0 {
		findings = append(findings, Finding{
			Severity: Warning,
			Member:   "tools",
			Reason:   `use_tools is false, so the tools list is never read`,
		})
	}
	if hasCmdBan && len(cmdBan) > 0 && !listsCommandTool(list) {
		for _, entry := range cmdBan {
			findings = append(findings, Finding{
				Severity: Warning,
				Member:   "cmd-ban",
				Reason:   fmt.Sprintf("cmd-ban lists %q, but neither \"cmd\" nor \"async_cmd\" is listed in tools, so no command runs to ban it", entry),
			})
		}
	}
	return findings
}

// toolReachable reports whether a tools entry names a tool the machine has,
// exactly or by glob.
func toolReachable(entry string, names []string) bool {
	if entry == "*" {
		return true
	}
	return slices.ContainsFunc(names, func(name string) bool {
		return entry == name || tools.WildcardMatch(entry, name)
	})
}

func listsCommandTool(list []string) bool {
	return slices.Contains(list, "cmd") || slices.Contains(list, "async_cmd")
}

// audioProblems requires audio.model to be a concrete model: an alias is
// refused there, because a transcription call cannot pick a vendor.
func (env Env) audioProblems(members map[string]jsontext.Value) Findings {
	audio, ok := objectMember(members, "audio")
	if !ok {
		return nil
	}
	model, ok := stringMember(audio, "model")
	if !ok || model == "" || env.ResolveModel == nil {
		return nil
	}
	candidates, err := env.ResolveModel(model)
	if err != nil || len(candidates) == 0 {
		return Findings{{
			Severity: Error,
			Member:   "audio.model",
			Reason:   fmt.Sprintf("audio.model %q routes to no model", model),
		}}
	}
	if slices.Equal(candidates, []string{model}) {
		return nil
	}
	return Findings{{
		Severity: Error,
		Member:   "audio.model",
		Reason:   fmt.Sprintf("audio.model %q is an alias; audio.model needs a concrete model", model),
	}}
}

// skillProblems requires every skill target to parse, and pins it on publish.
func skillProblems(members map[string]jsontext.Value) Findings {
	list, ok := stringsMember(members, "skills")
	if !ok {
		return nil
	}
	var findings Findings
	for _, target := range list {
		parsed, err := remote.ParseSkillTarget(target)
		if err != nil {
			findings = append(findings, Finding{
				Severity: Error,
				Member:   "skills",
				Reason:   fmt.Sprintf("skills: %s: %v", target, err),
			})
			continue
		}
		if parsed.Pinned() || parsed.SHA256 != "" {
			continue
		}
		findings = append(findings, Finding{
			Severity: Error,
			Member:   "skills",
			Routes:   []Route{Publish},
			Reason: fmt.Sprintf("skills: %s is not pinned; pin it with @<version>, @<ref> or #sha256=<hex>",
				target),
		})
	}
	return findings
}

// serverProblems checks what a server must say beyond the document: one
// transport, an envfile that has a directory to resolve against, and an auth
// block whose variable names are names.
func serverProblems(members map[string]jsontext.Value) Findings {
	servers, ok := objectMember(members, "mcp_servers")
	if !ok {
		return nil
	}
	var findings Findings
	for _, name := range slices.Sorted(maps.Keys(servers)) {
		fields, err := membersOf(servers[name])
		if err != nil {
			// The document refused the type; there is nothing to read.
			continue
		}
		prefix := "mcp_servers." + name
		command, _ := stringMember(fields, "command")
		url, _ := stringMember(fields, "url")
		if strings.TrimSpace(command) == "" && strings.TrimSpace(url) == "" {
			findings = append(findings, Finding{
				Severity: Error,
				Member:   prefix,
				Reason:   `needs "command" or "url"`,
			})
		}
		if envFile, _ := stringMember(fields, "envfile"); relativeEnvFile(envFile) {
			findings = append(findings, Finding{
				Severity: Error,
				Member:   prefix + ".envfile",
				Reason:   "a relative path has no profile directory to resolve against: use an absolute path, a ~/ path or a $variable",
			})
		}
		if rawAuth, ok := fields["auth"]; ok {
			findings = append(findings, authProblems(prefix, rawAuth, fields["env"])...)
		}
	}
	return findings
}

// authProblems checks a server's auth block without ever echoing a value: an
// entry that is not a variable name may be a pasted secret.
func authProblems(prefix string, rawAuth, rawEnv jsontext.Value) Findings {
	auth, err := membersOf(rawAuth)
	if err != nil {
		return nil
	}
	field := prefix + ".auth"
	var findings Findings
	if tokenEnv, ok := stringMember(auth, "token_env"); ok && !serverconfig.ValidEnvName(tokenEnv) {
		findings = append(findings, Finding{
			Severity: Error,
			Member:   field + ".token_env",
			Reason:   "token_env is not a valid environment variable name",
		})
	}
	required, ok := stringsMember(auth, "required_env")
	if !ok {
		return findings
	}
	env, _ := membersOf(rawEnv)
	first := make(map[string]int, len(required))
	for i, name := range required {
		at := fmt.Sprintf("%s.required_env[%d]", field, i)
		previous, seen := first[name]
		switch {
		case !serverconfig.ValidEnvName(name):
			findings = append(findings, Finding{
				Severity: Error,
				Member:   at,
				Reason:   "is not a valid environment variable name",
			})
		case seen:
			findings = append(findings, Finding{
				Severity: Error,
				Member:   at,
				Reason:   fmt.Sprintf("repeats required_env[%d]", previous),
			})
		case env[name] != nil:
			findings = append(findings, Finding{
				Severity: Error,
				Member:   at,
				Reason:   fmt.Sprintf("is also a key of %s.env, so the environment never supplies it", prefix),
			})
		}
		if !seen {
			first[name] = i
		}
	}
	return findings
}

// relativeEnvFile reports an envfile that has no directory to be relative to:
// not absolute, not ~, and no variable to expand.
func relativeEnvFile(path string) bool {
	path = strings.TrimSpace(path)
	return path != "" && !filepath.IsAbs(path) && path != "~" &&
		!strings.HasPrefix(path, "~/") && !strings.HasPrefix(path, "$")
}

func stringMember(members map[string]jsontext.Value, name string) (string, bool) {
	raw, ok := members[name]
	if !ok {
		return "", false
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", false
	}
	return value, true
}

func stringsMember(members map[string]jsontext.Value, name string) ([]string, bool) {
	raw, ok := members[name]
	if !ok {
		return nil, false
	}
	var value []string
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, false
	}
	return value, true
}

func boolMember(members map[string]jsontext.Value, name string) (bool, bool) {
	raw, ok := members[name]
	if !ok {
		return false, false
	}
	var value bool
	if err := json.Unmarshal(raw, &value); err != nil {
		return false, false
	}
	return value, true
}

func objectMember(members map[string]jsontext.Value, name string) (map[string]jsontext.Value, bool) {
	raw, ok := members[name]
	if !ok {
		return nil, false
	}
	members, err := membersOf(raw)
	if err != nil {
		return nil, false
	}
	return members, true
}

// sortFindings orders by member, then reason, then the name meant, so two runs
// over one body print the same list.
func sortFindings(findings Findings) {
	slices.SortStableFunc(findings, func(a, b Finding) int {
		if order := strings.Compare(a.Member, b.Member); order != 0 {
			return order
		}
		if order := strings.Compare(a.Reason, b.Reason); order != 0 {
			return order
		}
		return strings.Compare(a.Fixed, b.Fixed)
	})
}
