// Package schemacache persists a command-based MCP server's negotiated
// tools/list result, keyed by the content of its identity, so a lazy server
// can advertise its tools at setup without a transport being constructed
// (worklog 2026-10-02-mcp-connection-cost, phase 3, D18).
//
// Only content-determined data is ever written: a connect, auth or call
// failure is a fact about one run, never persisted here. A corrupt or
// unparseable entry is a miss, never an error; a write failure degrades to a
// warning at the caller, never fails setup.
package schemacache

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/baalimago/clai/internal/tools/mcp/launcher"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// FileFingerprint is the size-and-modification-time evidence for a file this
// package validates by delta rather than by content, following the
// convention already established for this repository's foreign conversation
// index. A nil *FileFingerprint is the canonical absent marker: distinct
// from a present, zero-size file.
type FileFingerprint struct {
	Size    int64     `json:"size"`
	ModTime time.Time `json:"mtime"`
}

// ExecutableFingerprint additionally carries the resolved path, since two
// different commands can resolve to the same size and modification time by
// coincidence but never to the same path.
type ExecutableFingerprint struct {
	Path    string    `json:"path"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"mtime"`
}

// LauncherFingerprint is the delta evidence for what a generic launcher's
// command line resolves to. Kind is the resolution's own classification, so a
// pinned version and a floating spec can never collide, and Spec names the
// resolved package. Installed carries size and modification time of the file
// the launcher installed, plus its path: a version bump rewrites that file,
// so an upgrade of an unpinned package is a miss by Key() rather than a wait
// out the freshness bound.
type LauncherFingerprint struct {
	Kind        launcher.Kind    `json:"kind"`
	Spec        string           `json:"spec"`
	Version     string           `json:"version,omitempty"`
	Installed   *FileFingerprint `json:"installed,omitempty"`
	InstallPath string           `json:"install_path,omitempty"`
}

// Identity pins everything that can change which tools a server exposes,
// command-based or endpoint-based. Two identities with the same JSON
// encoding are the same cache entry; the entry's filename is the hex
// SHA-256 of that encoding, so an identity change is a miss by construction,
// never by a separate comparison. Command and URL are never both set,
// mirroring the config's own XOR.
//
// ArgsDigest, not a verbatim Args, carries the command's arguments: the
// documented "mcp-remote --header Authorization: Bearer ..." shape puts a
// credential in args, and invariant 6 forbids a credential in a cached file
// (D38, R1-03). The identity still reacts to an args change because the
// digest changes with it.
type Identity struct {
	Command    string                 `json:"command"`
	ArgsDigest string                 `json:"args_digest"`
	URL        string                 `json:"url"`
	EnvDigest  string                 `json:"env_digest"`
	Envfile    *FileFingerprint       `json:"envfile,omitempty"`
	Executable *ExecutableFingerprint `json:"executable,omitempty"`
	// Script fingerprints the first args entry that resolves to an existing,
	// regular file: for an interpreter-launched server ("node", "python",
	// "uvx", ...) that is normally the server script itself, which
	// Executable never sees because it fingerprints only the launcher (D40,
	// R2-03). Absent when no args entry resolves to a file on disk (an npx
	// package name is not a path), in which case the identity still reacts
	// to the launcher's own delta exactly as before.
	Script *FileFingerprint `json:"script,omitempty"`
	// Launcher is what a generic launcher — an interpreter or a package
	// runner whose own binary is not the program that runs — would execute
	// for this command line, and how sure of it clai is. It is the component
	// that makes a warm cache hit self-verifying for the dominant "npx -y
	// <package>" shape: Pinned carries the exact version the config names,
	// Unpinned fingerprints the manifest the launcher installed. Absent for a
	// direct binary, for an interpreter given a script path (Script already
	// fingerprints the program), and for any command line whose resolution
	// failed, so an unresolvable server contributes nothing and its entry
	// stays byte-identical to one captured before this component existed.
	Launcher *LauncherFingerprint `json:"launcher,omitempty"`
	// Scopes is always nil for a command-based identity: phase 5 fills it
	// only for an endpoint-based, authorized server (D40's unification with
	// R1-16 — see BuildIdentityWithScopes).
	Scopes []string `json:"scopes,omitempty"`
}

// Key is the hex SHA-256 of identity's canonical JSON encoding, used as the
// entry's filename. Identity contains no maps, so json.Marshal's field order
// is stable and the encoding is deterministic.
func (id Identity) Key() (string, error) {
	b, err := json.Marshal(id)
	if err != nil {
		return "", fmt.Errorf("schema cache: encode identity: %w", err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

// BuildIdentity resolves server's identity. It never fails: an unresolvable
// executable or an unreadable envfile is encoded as the canonical absent
// marker rather than an error, so the lookup that follows is a miss and the
// real failure surfaces naturally when setup connects (D18).
func BuildIdentity(server pub_models.McpServer) Identity {
	return Identity{
		Command:    server.Command,
		ArgsDigest: argsDigest(server.Args),
		URL:        server.Url,
		EnvDigest:  envDigest(server.Env),
		Envfile:    resolveEnvfile(server.EnvFile),
		Executable: resolveExecutable(server.Command),
		Script:     resolveScriptFingerprint(server.Args),
		Launcher:   resolveLauncher(server),
	}
}

// resolveLauncher resolves what server's command line would execute and
// carries the result into the identity. A resolution clai cannot make is
// absent, never an error: the caller then holds no evidence of the content
// that will run, which is what makes the entry miss sooner.
func resolveLauncher(server pub_models.McpServer) *LauncherFingerprint {
	if server.Command == "" {
		return nil
	}
	res := launcher.Resolve(server)
	if res.Kind == launcher.Exact || res.Kind == launcher.Unresolvable {
		return nil
	}
	fp := &LauncherFingerprint{Kind: res.Kind, Spec: res.Spec, Version: res.PinnedVersion}
	if res.Installed != nil {
		fp.Installed = &FileFingerprint{Size: res.Installed.Size, ModTime: res.Installed.ModTime}
		fp.InstallPath = res.Installed.Path
	}
	return fp
}

// BuildIdentityWithScopes resolves server's identity exactly as BuildIdentity
// does, then fills the Scopes component from server.Auth.Scopes (phase 5):
// only for an endpoint-based server, since the scopes component belongs to
// the authorization flow phase 5 owns and a command-based server has no
// such flow. Without that discrimination, setup's BuildIdentity (which never
// looks at Auth at all) and the listing's BuildIdentityWithScopes computed
// different keys for a command-based server that merely declares an "auth"
// block, so the listing missed forever and silently (D40, R1-16). A server
// with no Auth block, or an Auth block requesting no particular scopes,
// carries no Scopes component either way, matching BuildIdentity's own
// output so an unauthorized server's cache entries are unaffected. A later
// change to the requested scopes changes the identity and is therefore a
// miss, by the same Key() mechanism as every other identity component.
func BuildIdentityWithScopes(server pub_models.McpServer) Identity {
	id := BuildIdentity(server)
	if server.Command == "" && server.Auth != nil {
		id.Scopes = server.Auth.Scopes
	}
	return id
}

// envDigest is the hex SHA-256 over env's sorted key=value lines, one per
// line, following the record-format description. The inherited process
// environment is never part of this: only env, the server's own configured
// map, is digested.
func envDigest(env map[string]string) string {
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var buf bytes.Buffer
	for _, k := range keys {
		fmt.Fprintf(&buf, "%s=%s\n", k, env[k])
	}
	sum := sha256.Sum256(buf.Bytes())
	return hex.EncodeToString(sum[:])
}

// argsDigest is the hex SHA-256 over args joined one per line, in order:
// order is part of a command line's meaning, so unlike envDigest this is
// never sorted. See Identity.ArgsDigest for why args is digested rather
// than stored verbatim.
func argsDigest(args []string) string {
	var buf bytes.Buffer
	for _, a := range args {
		buf.WriteString(a)
		buf.WriteByte('\n')
	}
	sum := sha256.Sum256(buf.Bytes())
	return hex.EncodeToString(sum[:])
}

func resolveEnvfile(path string) *FileFingerprint {
	fp, ok := launcher.FingerprintsFile(path)
	if !ok {
		return nil
	}
	return &FileFingerprint{Size: fp.Size, ModTime: fp.ModTime}
}

func resolveExecutable(command string) *ExecutableFingerprint {
	path, fp, ok := launcher.Executable(command)
	if !ok {
		return nil
	}
	return &ExecutableFingerprint{Path: path, Size: fp.Size, ModTime: fp.ModTime}
}

// resolveScriptFingerprint reports the FileFingerprint of the first args
// entry that resolves to an existing, regular file. See Identity.Script.
func resolveScriptFingerprint(args []string) *FileFingerprint {
	_, fp, ok := launcher.ScriptArg(args)
	if !ok {
		return nil
	}
	return &FileFingerprint{Size: fp.Size, ModTime: fp.ModTime}
}

// Record is the on-disk entry: an identity, the negotiated protocol version,
// the server info and tools/list result verbatim, and the capture time.
// ServerInfo and Tools are never interpreted here, only stored and returned.
type Record struct {
	Identity        Identity        `json:"identity"`
	ProtocolVersion string          `json:"protocol_version"`
	ServerInfo      json.RawMessage `json:"server_info"`
	Tools           json.RawMessage `json:"tools"`
	CapturedAt      time.Time       `json:"captured_at"`
}

// DefaultDirName is the schema-cache-directory parameter: the subdirectory
// of the clai cache dir the schema cache lives under (README parameters
// table, phase 3). Phase 7 reuses it so the tools listing's cache-only
// lookup resolves the same directory setup's own cache does.
const DefaultDirName = "mcpSchemas"

// DefaultFreshnessBound is the schema-cache-freshness-bound parameter: how
// long any entry stays valid with no corroborating signal, command-based or
// endpoint-based alike (D40). Delta validation on a command-based identity
// cannot see every form of change — an npx package upgrade touches no local
// file resolveExecutable or Script can fingerprint — so the time bound is
// the backstop for exactly the class of change the census shows dominates
// local servers (R2-03).
const DefaultFreshnessBound = 12 * time.Hour

// Cache is a directory of identity-keyed Records for MCP servers, command-
// based and endpoint-based alike. The zero value is not usable; construct
// with New.
type Cache struct {
	dir            string
	clock          func() time.Time
	freshnessBound time.Duration
}

// Option configures a Cache at construction.
type Option func(*Cache)

// WithClock overrides the clock Capture uses for CapturedAt and Lookup uses
// to judge an endpoint-based entry's freshness. Production code never needs
// this; every test that cares about either injects one instead of sleeping.
func WithClock(clock func() time.Time) Option {
	return func(c *Cache) { c.clock = clock }
}

// WithFreshnessBound overrides the schema-cache-freshness-bound parameter.
func WithFreshnessBound(d time.Duration) Option {
	return func(c *Cache) { c.freshnessBound = d }
}

// New builds a Cache rooted at dir. dir is required: a caller that omits it
// fails to construct rather than silently falling back to a real cache
// directory it never named.
func New(dir string, opts ...Option) (*Cache, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, fmt.Errorf("schema cache: directory is required")
	}
	c := &Cache{dir: dir, clock: time.Now, freshnessBound: DefaultFreshnessBound}
	for _, opt := range opts {
		opt(c)
	}
	return c, nil
}

// Lookup reports whether a valid entry exists for identity. A missing,
// corrupt, unparseable or mismatched entry is a miss, never an error: the
// caller always has a defined next step (connect).
func (c *Cache) Lookup(identity Identity) (Record, bool) {
	key, err := identity.Key()
	if err != nil {
		return Record{}, false
	}
	data, err := os.ReadFile(filepath.Join(c.dir, key+".json"))
	if err != nil {
		return Record{}, false
	}
	var rec Record
	if err := json.Unmarshal(data, &rec); err != nil {
		return Record{}, false
	}
	// Defence in depth: the filename is already the identity's hash, so a
	// mismatch here would mean a hash collision or a corrupted write. Either
	// way, treat it as a miss rather than trust the stale content.
	wantBytes, err := json.Marshal(identity)
	if err != nil {
		return Record{}, false
	}
	gotBytes, err := json.Marshal(rec.Identity)
	if err != nil || !bytes.Equal(wantBytes, gotBytes) {
		return Record{}, false
	}
	// The freshness bound applies to every entry, command-based or
	// endpoint-based (D40): an endpoint offers no local evidence at all that
	// its tool list changed, and a command-based identity's delta validation
	// is blind to a change that touches no fingerprinted local file (an npx
	// package upgrade). A clock that moves backwards makes this fail-open —
	// a negative duration can never exceed a positive bound — which is an
	// accepted optimisation consequence: an unexpired entry is only ever a
	// cache lookup, never a correctness surface (invariant 2, R1-36).
	if c.clock().Sub(rec.CapturedAt) > c.freshnessBound {
		return Record{}, false
	}
	return rec, true
}

// Invalidate removes identity's entry, if one exists. A missing entry is
// not an error: invalidation is idempotent, and the signals that trigger it
// (a tool-list-changed notification, an unknown-tool call failure) may fire
// more than once for the same entry. Invalidation never records a failure;
// it only ever removes content-determined data that is no longer trusted.
func (c *Cache) Invalidate(identity Identity) error {
	key, err := identity.Key()
	if err != nil {
		return fmt.Errorf("schema cache: key: %w", err)
	}
	if err := os.Remove(filepath.Join(c.dir, key+".json")); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("schema cache: invalidate: %w", err)
	}
	return nil
}

// Capture writes a Record for identity, synchronously and atomically
// (temp file plus rename), so a caller that returns from Capture has either
// a durable entry or a reported failure — never a half-written one. A
// failure is the caller's to degrade from; Capture never panics and never
// leaves a temp file behind.
//
// An empty tools array is refused rather than captured (sign-off review
// B4): a server that booted into a transient zero-tool state (toolsets not
// loaded, a container not ready, a gateway whose upstream has not
// connected) is a fact about that one run, not about the server, and this
// package's own rule is that a run-fact is never persisted — the same rule
// that already keeps a connect failure out of the cache. Without this
// guard, nothing can invalidate the resulting zero-tool entry before the
// freshness bound: the unknown-tool signal needs a tool to call, and the
// list-changed watcher needs a live connection a cache hit never dials.
func (c *Cache) Capture(identity Identity, protocolVersion string, serverInfo, tools json.RawMessage) error {
	if toolsArrayIsEmpty(tools) {
		return fmt.Errorf("schema cache: capture refused: empty tools array is a run-fact, not content (B4)")
	}
	key, err := identity.Key()
	if err != nil {
		return fmt.Errorf("schema cache: key: %w", err)
	}
	if err := os.MkdirAll(c.dir, 0o755); err != nil {
		return fmt.Errorf("schema cache: ensure directory %q: %w", c.dir, err)
	}

	rec := Record{
		Identity:        identity,
		ProtocolVersion: protocolVersion,
		ServerInfo:      serverInfo,
		Tools:           tools,
		CapturedAt:      c.clock(),
	}
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return fmt.Errorf("schema cache: encode record: %w", err)
	}

	tmp, err := os.CreateTemp(c.dir, key+"-*.tmp")
	if err != nil {
		return fmt.Errorf("schema cache: create temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("schema cache: write temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("schema cache: close temp file: %w", err)
	}
	if err := os.Rename(tmpPath, filepath.Join(c.dir, key+".json")); err != nil {
		return fmt.Errorf("schema cache: rename temp file: %w", err)
	}
	return nil
}

// toolsArrayIsEmpty reports whether tools decodes to a JSON array with no
// elements. An unparseable value is never reported empty here: Capture's
// caller only ever reaches it with a tools/list result that already
// unmarshalled successfully (RegisterTools ran first), so a decode failure
// in this helper is not this guard's concern.
func toolsArrayIsEmpty(tools json.RawMessage) bool {
	var arr []json.RawMessage
	if err := json.Unmarshal(tools, &arr); err != nil {
		return false
	}
	return len(arr) == 0
}

// ListEntry pairs one configured server's name with the cached Record a
// prior successful handshake left behind.
type ListEntry struct {
	ServerName string
	Record     Record
}

// ListCachedServers is phase 7's cache-only listing entry point: for each
// server in servers, in order, it builds the identity exactly as a live
// setup would (BuildIdentityWithScopes, which also covers a command-based
// server with no Auth block) and reports an entry only on a cache hit. A
// server with no hit — never run, or its last run failed — contributes
// nothing: the cache never records a run failure, so an entry is itself the
// evidence of success (D21). This never connects and never spawns; dir is
// the schema cache's own directory, not the clai cache dir it lives under.
func ListCachedServers(dir string, servers []pub_models.McpServer) ([]ListEntry, error) {
	cache, err := New(dir)
	if err != nil {
		return nil, err
	}
	var entries []ListEntry
	for _, server := range servers {
		rec, ok := cache.Lookup(BuildIdentityWithScopes(server))
		if !ok {
			continue
		}
		entries = append(entries, ListEntry{ServerName: server.Name, Record: rec})
	}
	return entries, nil
}
