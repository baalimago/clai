package schemacache

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/tools/mcp/launcher"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func writeExecutable(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("write executable %q: %v", path, err)
	}
}

// TestScopeChangeInvalidatesSchemaCacheEntry pins phase 5's extension of the
// endpoint-based identity: the requested scopes (auth.scopes) are part of
// it, so a scope change produces a different key and therefore a miss
// against an entry captured under the old scopes, by Key()'s own
// content-addressing mechanism — no separate comparison is needed.
func TestScopeChangeInvalidatesSchemaCacheEntry(t *testing.T) {
	dir := t.TempDir()
	cache, err := New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	server := pub_models.McpServer{Name: "srv", Url: "https://example.invalid/mcp", Auth: &pub_models.McpServerAuth{Scopes: []string{"read"}}}
	identity := BuildIdentityWithScopes(server)
	if err := cache.Capture(identity, "2025-06-18", []byte(`{}`), []byte(`[{"name":"t"}]`)); err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if _, ok := cache.Lookup(identity); !ok {
		t.Fatal("expected a hit for the identity just captured")
	}

	server.Auth.Scopes = []string{"read", "write"}
	changed := BuildIdentityWithScopes(server)
	changedKey, err := changed.Key()
	if err != nil {
		t.Fatalf("Key: %v", err)
	}
	originalKey, err := identity.Key()
	if err != nil {
		t.Fatalf("Key: %v", err)
	}
	if changedKey == originalKey {
		t.Fatal("identity key unchanged after a scope change")
	}
	if _, ok := cache.Lookup(changed); ok {
		t.Fatal("expected a miss for the identity with a different scopes component")
	}

	// A server with no Auth block, or an Auth block requesting no
	// particular scopes, is unaffected: BuildIdentityWithScopes produces
	// the same identity BuildIdentity does.
	plain := pub_models.McpServer{Name: "srv", Url: "https://example.invalid/mcp"}
	withScopesKey, err := BuildIdentityWithScopes(plain).Key()
	if err != nil {
		t.Fatalf("Key: %v", err)
	}
	plainKey, err := BuildIdentity(plain).Key()
	if err != nil {
		t.Fatalf("Key: %v", err)
	}
	if withScopesKey != plainKey {
		t.Fatal("BuildIdentityWithScopes diverged from BuildIdentity for a server with no Auth block")
	}
}

// TestCredentialNeverReachesSchemaCache pins the redaction table's schema
// cache row: BuildIdentityWithScopes exposes only the requested scopes,
// never a credential, even when the server config that produced the
// identity carries a secret-shaped auth.token_command argument. Capture's
// own record is checked the same way.
func TestCredentialNeverReachesSchemaCache(t *testing.T) {
	const secret = "planted-secret-value-should-never-be-cached"
	server := pub_models.McpServer{
		Name: "srv",
		Url:  "https://example.invalid/mcp",
		Auth: &pub_models.McpServerAuth{
			TokenCommand: []string{"sh", "-c", "echo " + secret},
			TokenEnv:     secret,
			Scopes:       []string{"read"},
		},
	}
	identity := BuildIdentityWithScopes(server)
	idJSON, err := json.Marshal(identity)
	if err != nil {
		t.Fatalf("Marshal identity: %v", err)
	}
	if strings.Contains(string(idJSON), secret) {
		t.Fatalf("identity leaks a secret: %s", idJSON)
	}

	dir := t.TempDir()
	cache, err := New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := cache.Capture(identity, "2025-06-18", []byte(`{}`), []byte(`[{"name":"t"}]`)); err != nil {
		t.Fatalf("Capture: %v", err)
	}
	key, err := identity.Key()
	if err != nil {
		t.Fatalf("Key: %v", err)
	}
	onDisk, err := os.ReadFile(filepath.Join(dir, key+".json"))
	if err != nil {
		t.Fatalf("read captured entry: %v", err)
	}
	if strings.Contains(string(onDisk), secret) {
		t.Fatalf("captured record leaks a secret: %s", onDisk)
	}
}

func TestSchemaCacheKeyIsSha256OfIdentity(t *testing.T) {
	id := Identity{Command: "node", ArgsDigest: argsDigest([]string{"server.js"}), EnvDigest: "abc"}
	got, err := id.Key()
	if err != nil {
		t.Fatalf("Key: %v", err)
	}
	b, err := json.Marshal(id)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	sum := sha256.Sum256(b)
	want := hex.EncodeToString(sum[:])
	if got != want {
		t.Errorf("Key() = %q, want sha256 of the identity's JSON encoding %q", got, want)
	}
	if len(got) != 64 {
		t.Errorf("Key() length = %d, want 64 (hex sha256)", len(got))
	}
}

// TestBuildIdentityHandlesEmptyCommandAndMissingEnvfile exercises the two
// absent-marker branches TestSchemaCacheUnresolvableExecutableIsMiss does
// not reach: an empty command (no executable to resolve at all) and an
// envfile path that is configured but does not exist on disk.
func TestBuildIdentityHandlesEmptyCommandAndMissingEnvfile(t *testing.T) {
	id := BuildIdentity(pub_models.McpServer{EnvFile: filepath.Join(t.TempDir(), "missing.env")})
	if id.Executable != nil {
		t.Errorf("Executable = %+v, want nil for an empty command", id.Executable)
	}
	if id.Envfile != nil {
		t.Errorf("Envfile = %+v, want nil for a configured-but-missing envfile", id.Envfile)
	}
}

// TestSchemaCacheCaptureFailsWhenDirectoryCannotBeCreated pins that a
// directory which can never be created (a file already occupies its name)
// surfaces as an error from Capture, the degrade-to-warning case the setup
// call site handles.
func TestSchemaCacheCaptureFailsWhenDirectoryCannotBeCreated(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "blocked")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatalf("write blocking file: %v", err)
	}
	c, err := New(filepath.Join(blocker, "mcpSchemas"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := c.Capture(Identity{Command: "node"}, "v1", json.RawMessage(`{}`), json.RawMessage(`[]`)); err == nil {
		t.Fatal("Capture succeeded although its directory can never be created")
	}
}

func TestCacheConstructorRequiresDirectory(t *testing.T) {
	if _, err := New(""); err == nil {
		t.Fatal("New(\"\") succeeded, want an error: the directory is required, not defaulted")
	}
	if _, err := New("   "); err == nil {
		t.Fatal("New of a blank directory succeeded, want an error")
	}
}

func TestSchemaCacheMissOnBinarySizeDelta(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "server.sh")
	writeExecutable(t, bin, "#!/bin/sh\necho hi\n")
	server := pub_models.McpServer{Command: bin}

	before := BuildIdentity(server)
	writeExecutable(t, bin, "#!/bin/sh\necho hi, now with a longer body so the size changes\n")
	after := BuildIdentity(server)

	keyBefore, err := before.Key()
	if err != nil {
		t.Fatalf("Key: %v", err)
	}
	keyAfter, err := after.Key()
	if err != nil {
		t.Fatalf("Key: %v", err)
	}
	if keyBefore == keyAfter {
		t.Fatal("executable size delta did not change the identity key")
	}
}

func TestSchemaCacheMissOnBinaryMtimeDelta(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "server.sh")
	writeExecutable(t, bin, "#!/bin/sh\necho hi\n")
	server := pub_models.McpServer{Command: bin}

	before := BuildIdentity(server)
	newTime := time.Now().Add(1 * time.Hour)
	if err := os.Chtimes(bin, newTime, newTime); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}
	after := BuildIdentity(server)

	keyBefore, _ := before.Key()
	keyAfter, _ := after.Key()
	if keyBefore == keyAfter {
		t.Fatal("executable mtime delta did not change the identity key")
	}
}

func TestSchemaCacheMissOnEnvfileDelta(t *testing.T) {
	dir := t.TempDir()
	envfile := filepath.Join(dir, "x.env")
	if err := os.WriteFile(envfile, []byte("A=1\n"), 0o600); err != nil {
		t.Fatalf("write envfile: %v", err)
	}
	server := pub_models.McpServer{Command: "node", EnvFile: envfile}

	before := BuildIdentity(server)
	if err := os.WriteFile(envfile, []byte("A=1\nB=2\n"), 0o600); err != nil {
		t.Fatalf("rewrite envfile: %v", err)
	}
	after := BuildIdentity(server)

	keyBefore, _ := before.Key()
	keyAfter, _ := after.Key()
	if keyBefore == keyAfter {
		t.Fatal("envfile size delta did not change the identity key")
	}
}

func TestSchemaCacheMissOnEnvHashChange(t *testing.T) {
	a := BuildIdentity(pub_models.McpServer{Command: "node", Env: map[string]string{"KEY": "one"}})
	b := BuildIdentity(pub_models.McpServer{Command: "node", Env: map[string]string{"KEY": "two"}})

	keyA, _ := a.Key()
	keyB, _ := b.Key()
	if keyA == keyB {
		t.Fatal("env map change did not change the identity key")
	}
}

// TestSchemaCacheUnresolvableExecutableIsMiss pins that an unresolvable
// command never fails identity-building: the executable component is the
// canonical absent marker, and since no capture could ever have succeeded
// for that server, the lookup is always a miss.
func TestSchemaCacheUnresolvableExecutableIsMiss(t *testing.T) {
	server := pub_models.McpServer{Command: "/definitely-not-a-real-binary-xyz123"}
	id := BuildIdentity(server)
	if id.Executable != nil {
		t.Fatalf("Executable = %+v, want nil (canonical absent marker)", id.Executable)
	}

	c, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, ok := c.Lookup(id); ok {
		t.Fatal("unresolvable executable produced a hit on an empty cache")
	}
}

func TestSchemaCacheCorruptEntryIsTreatedAsMiss(t *testing.T) {
	dir := t.TempDir()
	c, err := New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	id := Identity{Command: "node"}
	key, err := id.Key()
	if err != nil {
		t.Fatalf("Key: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, key+".json"), []byte("{not valid json"), 0o644); err != nil {
		t.Fatalf("write corrupt entry: %v", err)
	}

	if _, ok := c.Lookup(id); ok {
		t.Fatal("corrupt entry treated as a hit")
	}
}

// TestSchemaCacheRefusesEmptyToolsArray pins the sign-off review's B4: an
// empty tools/list result is a run-fact about one transient handshake (a
// server that booted into a zero-tool state), not content about the
// server, so Capture must refuse it rather than latch "zero tools" for the
// freshness bound. Reproduces the review's own probe: without the guard,
// Capture(empty) accepted=true, Lookup hit=true, tools=[].
func TestSchemaCacheRefusesEmptyToolsArray(t *testing.T) {
	dir := t.TempDir()
	c, err := New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	identity := Identity{Command: "node"}
	if err := c.Capture(identity, "v1", json.RawMessage(`{}`), json.RawMessage(`[]`)); err == nil {
		t.Fatal("Capture accepted an empty tools array, want a refusal (B4)")
	}
	if _, ok := c.Lookup(identity); ok {
		t.Fatal("Lookup hit after an empty-tools Capture was refused; no entry should have been written")
	}
}

// TestSchemaCachePartialWriteLeavesNoEntry forces Capture's final rename to
// fail (by pre-occupying the entry path with a non-empty directory, which a
// rename can never replace) after the temp file was fully written, and
// checks that neither a stray temp file nor a corrupted final entry remains.
func TestSchemaCachePartialWriteLeavesNoEntry(t *testing.T) {
	dir := t.TempDir()
	c, err := New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	id := Identity{Command: "node"}
	key, err := id.Key()
	if err != nil {
		t.Fatalf("Key: %v", err)
	}
	finalPath := filepath.Join(dir, key+".json")
	if err := os.Mkdir(finalPath, 0o755); err != nil {
		t.Fatalf("pre-occupy entry path with a directory: %v", err)
	}

	if err := c.Capture(id, "v1", json.RawMessage(`{}`), json.RawMessage(`[]`)); err == nil {
		t.Fatal("Capture succeeded although the entry path was a directory")
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if e.Name() != filepath.Base(finalPath) {
			t.Errorf("stray file left behind after a failed write: %v", e.Name())
		}
	}
	info, statErr := os.Stat(finalPath)
	if statErr != nil || !info.IsDir() {
		t.Error("the pre-existing entry path was corrupted by the failed write")
	}
}

// assertFreshnessBoundApplies pins the shared freshness-bound shape an
// endpoint-based and a command-based identity now both follow (D40): an
// entry inside the bound is a hit, the same entry past it is a miss. Shared
// so the http and the command-based half don't dupl-clone one another.
func assertFreshnessBoundApplies(t *testing.T, id Identity) {
	t.Helper()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	clock := now
	c, err := New(t.TempDir(), WithClock(func() time.Time { return clock }), WithFreshnessBound(12*time.Hour))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := c.Capture(id, "v1", json.RawMessage(`{}`), json.RawMessage(`[{"name":"t"}]`)); err != nil {
		t.Fatalf("Capture: %v", err)
	}

	clock = now.Add(11 * time.Hour)
	if _, ok := c.Lookup(id); !ok {
		t.Fatal("entry inside the freshness bound reported a miss")
	}

	clock = now.Add(13 * time.Hour)
	if _, ok := c.Lookup(id); ok {
		t.Fatal("entry past the freshness bound reported a hit")
	}
}

// TestHttpSchemaCacheEntryExpiresOnFreshnessBound pins the endpoint-based
// half of the identity (phase 4): an entry beyond the freshness bound is a
// miss, even though its identity and content are unchanged, because an
// endpoint offers no local evidence that its tool list changed.
func TestHttpSchemaCacheEntryExpiresOnFreshnessBound(t *testing.T) {
	assertFreshnessBoundApplies(t, Identity{URL: "https://mcp.example.com/mcp"})
}

// TestSchemaCacheFreshnessBoundAppliesToCommandIdentityToo pins D40's
// correction of R2-03: a command-based identity's delta validation cannot
// see every form of server change (an npx package upgrade touches no local
// file this package fingerprints), so the freshness bound now applies to
// every entry, not only an endpoint-based one. Phase 3 originally shipped
// the opposite rule; this test replaces the one that pinned it.
func TestSchemaCacheFreshnessBoundAppliesToCommandIdentityToo(t *testing.T) {
	assertFreshnessBoundApplies(t, Identity{Command: "node"})
}

// TestSchemaCacheMissOnScriptFileDelta pins D40's fix for R2-03's probe:
// for an interpreter-launched server ("node", "python", ...), resolveExecutable
// fingerprints only the launcher, which never changes when the server script
// is rewritten. Script closes that gap by fingerprinting the first args
// entry that resolves to a file on disk.
func TestSchemaCacheMissOnScriptFileDelta(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "server.js")
	if err := os.WriteFile(script, []byte("console.log('v1')"), 0o644); err != nil {
		t.Fatalf("write script: %v", err)
	}
	server := pub_models.McpServer{Command: "node", Args: []string{script}}

	before := BuildIdentity(server)
	if before.Script == nil {
		t.Fatal("Script fingerprint absent for an args entry that resolves to an existing file")
	}

	if err := os.WriteFile(script, []byte("console.log('v2, now longer')"), 0o644); err != nil {
		t.Fatalf("rewrite script: %v", err)
	}
	after := BuildIdentity(server)

	keyBefore, _ := before.Key()
	keyAfter, _ := after.Key()
	if keyBefore == keyAfter {
		t.Fatal("rewriting the launched script did not change the identity key")
	}
}

// TestSchemaCacheScriptFingerprintAbsentWhenNoArgResolvesToAFile pins that a
// launcher invocation with no on-disk script argument (an npx package name
// is not a path) leaves Script absent rather than erroring, matching
// BuildIdentity's infallible contract.
func TestSchemaCacheScriptFingerprintAbsentWhenNoArgResolvesToAFile(t *testing.T) {
	server := pub_models.McpServer{Command: "npx", Args: []string{"-y", "@modelcontextprotocol/server-filesystem", "/tmp"}}
	id := BuildIdentity(server)
	if id.Script != nil {
		t.Fatalf("Script = %+v, want nil: no arg resolves to a real file", id.Script)
	}
}

// TestSchemaCacheArgsAreDigestedNotStoredVerbatim pins D38/R1-03: a secret
// written into args (the documented "mcp-remote --header Authorization:
// Bearer ..." shape) must never reach the identity verbatim, while an args
// change must still change the key.
func TestSchemaCacheArgsAreDigestedNotStoredVerbatim(t *testing.T) {
	const secret = "sk-live-planted-secret-value"
	server := pub_models.McpServer{Command: "npx", Args: []string{"-y", "mcp-remote", "https://x/mcp", "--header", "Authorization: Bearer " + secret}}
	id := BuildIdentity(server)

	idJSON, err := json.Marshal(id)
	if err != nil {
		t.Fatalf("Marshal identity: %v", err)
	}
	if strings.Contains(string(idJSON), secret) {
		t.Fatalf("identity leaks a secret from args: %s", idJSON)
	}
	if id.ArgsDigest == "" {
		t.Fatal("ArgsDigest is empty")
	}

	changed := server
	changed.Args = append([]string{}, server.Args...)
	changed.Args[len(changed.Args)-1] = "Authorization: Bearer sk-live-different-secret-value"
	changedID := BuildIdentity(changed)
	key, _ := id.Key()
	changedKey, _ := changedID.Key()
	if key == changedKey {
		t.Fatal("an args change did not change the identity key")
	}
}

// TestSchemaCacheCommandServerIgnoresAuthScopes pins D40's unification of
// R1-16: a command-based server declaring an "auth" block (which
// validateTransport does not reject) must produce the same identity under
// BuildIdentityWithScopes as under BuildIdentity, so setup and the cache-only
// listing can never compute different keys for it.
func TestSchemaCacheCommandServerIgnoresAuthScopes(t *testing.T) {
	server := pub_models.McpServer{
		Command: "node",
		Args:    []string{"s.js"},
		Auth:    &pub_models.McpServerAuth{Scopes: []string{"read"}},
	}
	plain := BuildIdentity(server)
	withScopes := BuildIdentityWithScopes(server)

	plainKey, _ := plain.Key()
	withScopesKey, _ := withScopes.Key()
	if plainKey != withScopesKey {
		t.Fatalf("BuildIdentityWithScopes diverged from BuildIdentity for a command-based server: %q != %q", withScopesKey, plainKey)
	}
	if withScopes.Scopes != nil {
		t.Fatalf("Scopes = %v, want nil for a command-based server", withScopes.Scopes)
	}
}

// TestHttpSchemaCacheMissOnEnvOrEnvfileDelta pins that the endpoint identity
// carries the same environment and envfile components as the command-based
// identity, so a change to either is a miss by construction, independent of
// the URL.
func TestHttpSchemaCacheMissOnEnvOrEnvfileDelta(t *testing.T) {
	dir := t.TempDir()
	envfile := filepath.Join(dir, "x.env")
	if err := os.WriteFile(envfile, []byte("A=1\n"), 0o600); err != nil {
		t.Fatalf("write envfile: %v", err)
	}
	server := pub_models.McpServer{Url: "https://mcp.example.com/mcp", EnvFile: envfile}
	before := BuildIdentity(server)
	if before.URL == "" {
		t.Fatal("BuildIdentity did not carry Url onto the identity")
	}

	// Env map change.
	afterEnv := BuildIdentity(pub_models.McpServer{Url: server.Url, EnvFile: envfile, Env: map[string]string{"K": "v"}})
	beforeKey, _ := before.Key()
	afterEnvKey, _ := afterEnv.Key()
	if beforeKey == afterEnvKey {
		t.Fatal("env map change did not change the endpoint identity key")
	}

	// Envfile delta.
	if err := os.WriteFile(envfile, []byte("A=1\nB=2\n"), 0o600); err != nil {
		t.Fatalf("rewrite envfile: %v", err)
	}
	afterEnvfile := BuildIdentity(server)
	afterEnvfileKey, _ := afterEnvfile.Key()
	if beforeKey == afterEnvfileKey {
		t.Fatal("envfile size delta did not change the endpoint identity key")
	}
}

// TestSchemaCacheInvalidateRemovesEntry pins Invalidate's own contract: a
// present entry is gone afterwards, and invalidating a never-captured
// identity is not an error (idempotent).
func TestSchemaCacheInvalidateRemovesEntry(t *testing.T) {
	c, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	id := Identity{URL: "https://mcp.example.com/mcp"}
	if err := c.Capture(id, "v1", json.RawMessage(`{}`), json.RawMessage(`[{"name":"t"}]`)); err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if _, ok := c.Lookup(id); !ok {
		t.Fatal("expected a hit before Invalidate")
	}

	if err := c.Invalidate(id); err != nil {
		t.Fatalf("Invalidate: %v", err)
	}
	if _, ok := c.Lookup(id); ok {
		t.Fatal("entry still present after Invalidate")
	}

	if err := c.Invalidate(id); err != nil {
		t.Fatalf("Invalidate on an already-absent entry must not error: %v", err)
	}
}

// TestSchemaCacheBackwardClockRecordsCaptureTimeVerbatim pins that no
// validity decision in this phase depends on the clock moving forward: the
// capture time is stored exactly as the injected clock reports it.
func TestSchemaCacheBackwardClockRecordsCaptureTimeVerbatim(t *testing.T) {
	later := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	earlier := later.Add(-24 * time.Hour)
	clock := later

	c, err := New(t.TempDir(), WithClock(func() time.Time { return clock }))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	id := Identity{Command: "node"}

	if err := c.Capture(id, "v", json.RawMessage(`{}`), json.RawMessage(`[{"name":"t"}]`)); err != nil {
		t.Fatalf("first Capture: %v", err)
	}
	clock = earlier
	if err := c.Capture(id, "v", json.RawMessage(`{}`), json.RawMessage(`[{"name":"t"}]`)); err != nil {
		t.Fatalf("second Capture: %v", err)
	}

	rec, ok := c.Lookup(id)
	if !ok {
		t.Fatal("expected a hit after Capture")
	}
	if !rec.CapturedAt.Equal(earlier) {
		t.Errorf("CapturedAt = %v, want %v (the backward clock value, recorded verbatim)", rec.CapturedAt, earlier)
	}
}

// TestListCachedServers_ReturnsOnlyHits pins phase 7's cache-only listing
// entry point directly: a server with a cache entry is reported with its
// Record, a server with none contributes nothing, and the order follows
// servers, not insertion order into the cache.
func TestListCachedServers_ReturnsOnlyHits(t *testing.T) {
	dir := t.TempDir()
	cache, err := New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	hit := pub_models.McpServer{Name: "hit", Command: "node"}
	miss := pub_models.McpServer{Name: "miss", Command: "python"}
	if err := cache.Capture(BuildIdentityWithScopes(hit), "2025-06-18", json.RawMessage(`{}`), json.RawMessage(`[{"name":"t"}]`)); err != nil {
		t.Fatalf("Capture: %v", err)
	}

	entries, err := ListCachedServers(dir, []pub_models.McpServer{miss, hit})
	if err != nil {
		t.Fatalf("ListCachedServers: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected exactly 1 entry, got %d: %+v", len(entries), entries)
	}
	if entries[0].ServerName != "hit" {
		t.Fatalf("ServerName = %q, want %q", entries[0].ServerName, "hit")
	}
	var gotTools []map[string]string
	if err := json.Unmarshal(entries[0].Record.Tools, &gotTools); err != nil {
		t.Fatalf("decode Record.Tools: %v", err)
	}
	if len(gotTools) != 1 || gotTools[0]["name"] != "t" {
		t.Fatalf("Record.Tools decoded to %+v, want the captured tools array", gotTools)
	}
}

// TestListCachedServers_EmptyDirectoryYieldsNoEntries pins that a schema
// cache directory that has never been written to (no run has warmed it)
// is a plain miss for every server, never an error.
func TestListCachedServers_EmptyDirectoryYieldsNoEntries(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "never-created")
	servers := []pub_models.McpServer{{Name: "fs", Command: "node"}}

	entries, err := ListCachedServers(dir, servers)
	if err != nil {
		t.Fatalf("ListCachedServers: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected no entries, got %+v", entries)
	}
}

// TestIdentityCarriesPinnedLauncherVersion pins that the launcher resolution
// reaches the identity: for an npx command line naming one exact version the
// version is part of the key, so upgrading a pinned server is a miss by
// construction rather than a wait out the freshness bound. This is the
// component that removes the need for the per-run notice sign-off review B3
// added for the unpinned launcher-only shape.
func TestIdentityCarriesPinnedLauncherVersion(t *testing.T) {
	t.Setenv("NPM_CONFIG_CACHE", filepath.Join(t.TempDir(), "does-not-exist"))
	old := BuildIdentity(pub_models.McpServer{Command: "npx", Args: []string{"-y", "slivingdoc@1.0.2", "serve"}})
	newer := BuildIdentity(pub_models.McpServer{Command: "npx", Args: []string{"-y", "slivingdoc@1.0.3", "serve"}})

	if old.Launcher == nil {
		t.Fatal("Launcher is nil for a pinned npx spec")
	}
	if old.Launcher.Kind != launcher.Pinned {
		t.Errorf("Launcher.Kind = %v, want pinned", old.Launcher.Kind)
	}
	if old.Launcher.Version != "1.0.2" {
		t.Errorf("Launcher.Version = %q, want %q", old.Launcher.Version, "1.0.2")
	}
	if old.Launcher.Spec != "slivingdoc" {
		t.Errorf("Launcher.Spec = %q, want %q", old.Launcher.Spec, "slivingdoc")
	}
	oldKey, err := old.Key()
	if err != nil {
		t.Fatalf("Key: %v", err)
	}
	newKey, err := newer.Key()
	if err != nil {
		t.Fatalf("Key: %v", err)
	}
	if oldKey == newKey {
		t.Error("a pinned version bump produced the same identity key")
	}
}

// TestIdentityCarriesInstalledManifestFingerprint pins the unpinned half: an
// npx command line naming a package the launcher has installed contributes
// the installed manifest's delta evidence, and rewriting that manifest — what
// an upgrade does — changes the key.
func TestIdentityCarriesInstalledManifestFingerprint(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv("NPM_CONFIG_CACHE", cacheDir)
	installDir := launcher.NpxInstallDir([]string{"slivingdoc"}, cacheDir)
	if installDir == "" {
		t.Fatal("NpxInstallDir returned empty under a configured cache root")
	}
	if err := os.MkdirAll(installDir, 0o755); err != nil {
		t.Fatalf("mkdir install dir: %v", err)
	}
	writeInstallManifest(t, installDir, "slivingdoc", "1.0.2")

	server := pub_models.McpServer{Command: "npx", Args: []string{"-y", "slivingdoc", "serve"}}
	before := BuildIdentity(server)
	if before.Launcher == nil {
		t.Fatal("Launcher is nil for an installed, unpinned npx package")
	}
	if before.Launcher.Kind != launcher.Unpinned {
		t.Errorf("Launcher.Kind = %v, want unpinned", before.Launcher.Kind)
	}
	if before.Launcher.Installed == nil {
		t.Fatal("Launcher.Installed is nil for an installed package")
	}

	writeInstallManifest(t, installDir, "slivingdoc", "1.0.3")

	after := BuildIdentity(server)
	beforeKey, err := before.Key()
	if err != nil {
		t.Fatalf("Key: %v", err)
	}
	afterKey, err := after.Key()
	if err != nil {
		t.Fatalf("Key: %v", err)
	}
	if beforeKey == afterKey {
		t.Error("an installed package upgrade produced the same identity key")
	}
}

// TestIdentityAbsentLauncherStaysStable pins the migration property: a
// server whose command line cannot be resolved contributes no launcher
// component, so its identity is byte-identical to one built before this
// component existed. Without it every unresolvable server would strand the
// entries captured by an earlier clai and silently reconnect forever.
func TestIdentityAbsentLauncherStaysStable(t *testing.T) {
	t.Setenv("NPM_CONFIG_CACHE", t.TempDir())
	for _, server := range []pub_models.McpServer{
		{Name: "opaque", Command: "docker", Args: []string{"run", "img:1"}},
		{Name: "uninstalled", Command: "npx", Args: []string{"-y", "never-installed-pkg", "serve"}},
		{Name: "endpoint", Url: "https://example.invalid/mcp"},
	} {
		id := BuildIdentity(server)
		if id.Launcher != nil {
			t.Errorf("%s: Launcher = %+v, want nil for an unresolvable command line", server.Name, id.Launcher)
		}
	}
}

// TestIdentityDirectBinaryCarriesNoLauncherComponent pins that a directly
// executed binary is not re-resolved: its Executable component already
// fingerprints the program that runs, so a second component would say
// nothing and cost a lookup on every setup.
func TestIdentityDirectBinaryCarriesNoLauncherComponent(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "server")
	writeExecutable(t, bin, "#!/bin/sh\nexit 0\n")
	id := BuildIdentity(pub_models.McpServer{Command: bin})
	if id.Executable == nil {
		t.Fatal("Executable is nil for a direct binary")
	}
	if id.Launcher != nil {
		t.Errorf("Launcher = %+v, want nil for a direct binary", id.Launcher)
	}
}

// TestIdentityLauncherCarriesNoCredential pins the redaction table on the new
// component: the resolved spec is a package name and the version is a version,
// neither of which can hold a secret, and the digest of args — where a
// bearer token would live — still travels as a digest rather than verbatim.
func TestIdentityLauncherCarriesNoCredential(t *testing.T) {
	t.Setenv("NPM_CONFIG_CACHE", filepath.Join(t.TempDir(), "does-not-exist"))
	id := BuildIdentity(pub_models.McpServer{
		Command: "npx",
		Args:    []string{"-y", "mcp-remote@1.0.0", "--header", "Authorization: Bearer super-secret-token"},
	})
	if id.Launcher != nil && strings.Contains(fmt.Sprintf("%+v", *id.Launcher), "super-secret-token") {
		t.Error("the launcher component carries a credential from args")
	}
	if strings.Contains(string(mustEncode(t, id)), "super-secret-token") {
		t.Error("the encoded identity carries a credential from args")
	}
}

func mustEncode(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return data
}

// writeInstallManifest materialises the root manifest and lockfile an npx
// install directory carries, declaring spec at version.
func writeInstallManifest(t *testing.T, dir, spec, version string) {
	t.Helper()
	root := `{"dependencies":{"` + spec + `":"^` + version + `"},"_npx":{"packages":["` + spec + `@latest"]}}`
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(root), 0o644); err != nil {
		t.Fatalf("write install package.json: %v", err)
	}
	lock := `{"name":"` + filepath.Base(dir) + `","lockfileVersion":3,"packages":{"":{},"node_modules/` + spec + `":{"version":"` + version + `"}}}`
	if err := os.WriteFile(filepath.Join(dir, "package-lock.json"), []byte(lock), 0o644); err != nil {
		t.Fatalf("write install package-lock.json: %v", err)
	}
}
