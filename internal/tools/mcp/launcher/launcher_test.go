package launcher

import (
	"os"
	"path/filepath"
	"testing"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// TestSpecIsPinned pins the pure, I/O-free half of Resolve: whether a
// package spec names one exact version, which is the only form whose
// resolved content is knowable from the config alone. Everything that
// floats — a bare name, a dist-tag, a partial version, a caret or tilde
// range — must report false, so the caller falls through to the install
// directory rather than trusting a version nobody read.
func TestSpecIsPinned(t *testing.T) {
	tests := []struct {
		spec string
		want bool
	}{
		{spec: "slivingdoc@1.0.2", want: true},
		{spec: "slivingdoc@=1.0.2", want: true},
		{spec: "slivingdoc@1.0.2-rc10", want: true},
		{spec: "@scope/pkg@2.5.0", want: true},
		{spec: "@scope/pkg@0.0.0-experimental.4", want: true},
		{spec: "pkg@10.20.30", want: true},

		{spec: "slivingdoc", want: false},
		{spec: "slivingdoc@latest", want: false},
		{spec: "slivingdoc@next", want: false},
		{spec: "slivingdoc@1", want: false},
		{spec: "slivingdoc@1.0", want: false},
		{spec: "slivingdoc@^1.0.2", want: false},
		{spec: "slivingdoc@~1.0.2", want: false},
		{spec: "slivingdoc@>=1.0.0", want: false},
		{spec: "slivingdoc@*", want: false},
		{spec: "slivingdoc@1.x", want: false},
		{spec: "pkg@1.2.3 || 2.0.0", want: false},
		{spec: "pkg@1.2.3.4", want: false},
		{spec: "pkg@v1.2.3", want: false},
		{spec: "pkg@1.2.3-beta+b1", want: false},
		{spec: "pkg@file:/tmp/pkg", want: false},
		{spec: "pkg@git+ssh://git@host/pkg.git", want: false},
		{spec: "pkg@workspace:*", want: false},
		{spec: "pkg@npm:other@1.0.0", want: false},
		{spec: "pkg@", want: false},
		{spec: "@scope/pkg", want: false},
		{spec: "", want: false},
	}
	for _, tt := range tests {
		if got := SpecIsPinned(tt.spec); got != tt.want {
			t.Errorf("SpecIsPinned(%q) = %v, want %v", tt.spec, got, tt.want)
		}
	}
}

// TestNpxInstallDirMatchesLibnpmexec pins the one thing this package
// reimplements: libnpmexec names an npx install directory by the first 16
// hex characters of the sha512 of the joined, sorted package specs. The
// digest is a hard contract with the on-disk layout clai then reads, so the
// expected name is derived from that algorithm rather than recorded from a
// machine this test never runs on.
func TestNpxInstallDirMatchesLibnpmexec(t *testing.T) {
	dir := NpxInstallDir([]string{"slivingdoc"}, "/cache-root")
	if dir == "" {
		t.Fatal("NpxInstallDir returned empty for a resolvable spec set")
	}
	// sha512("slivingdoc")[:16] == 7179fbb5ae509a5f
	if filepath.Base(dir) != "7179fbb5ae509a5f" {
		t.Errorf("install dir = %q, want the sha512(spec)[:16] directory name", filepath.Base(dir))
	}
}

// TestNpxInstallDirIsOrderInsensitive pins that the digest covers the spec
// set rather than the argument order, matching libnpmexec's own sort before
// hashing. A launcher whose resolution is order-independent must not produce
// an identity that changes when a config's argument order does.
func TestNpxInstallDirIsOrderInsensitive(t *testing.T) {
	a := NpxInstallDir([]string{"a-pkg", "b-pkg"}, "/cache-root")
	b := NpxInstallDir([]string{"b-pkg", "a-pkg"}, "/cache-root")
	if a == "" || b == "" {
		t.Fatal("NpxInstallDir returned empty for a resolvable spec set")
	}
	if a != b {
		t.Errorf("NpxInstallDir order changed the install dir: %q vs %q", a, b)
	}
}

// TestNpxInstallDirEmptyWithoutCacheRoot pins the absent-marker contract: a
// host with no npm cache root resolvable must produce no path at all rather
// than a guess, since a wrong path would fingerprint the wrong tree.
func TestNpxInstallDirEmptyWithoutCacheRoot(t *testing.T) {
	if got := NpxInstallDir([]string{"slivingdoc"}, ""); got != "" {
		t.Errorf("NpxInstallDir = %q, want empty when no npm cache root is resolvable", got)
	}
	if got := NpxInstallDir(nil, "/cache-root"); got != "" {
		t.Errorf("NpxInstallDir(nil) = %q, want empty for no specs", got)
	}
	if got := NpxInstallDir([]string{"", "pkg"}, "/cache-root"); got != "" {
		t.Errorf("NpxInstallDir with an empty spec = %q, want empty", got)
	}
}

// TestResolveUnresolvableForUnmodelledCommand pins that a command clai does
// not model never claims a resolution. Defaulting to Unresolvable is what
// makes an unknown future launcher safe without anyone editing a table.
func TestResolveUnresolvableForUnmodelledCommand(t *testing.T) {
	t.Setenv("NPM_CONFIG_CACHE", t.TempDir())
	for _, cmd := range []string{"docker", "bunx", "pipx", "deno", "uvx", "uv", "/opt/custom/server", "node"} {
		got := Resolve(pub_models.McpServer{Command: cmd, Args: []string{"serve"}})
		if got.Kind != Unresolvable {
			t.Errorf("Resolve(command=%q).Kind = %v, want Unresolvable", cmd, got.Kind)
		}
		if got.Installed != nil {
			t.Errorf("Resolve(command=%q).Installed = %+v, want nil", cmd, got.Installed)
		}
		if got.PinnedVersion != "" {
			t.Errorf("Resolve(command=%q).PinnedVersion = %q, want empty", cmd, got.PinnedVersion)
		}
	}
}

// TestResolveUnresolvableForNonNpxShapes pins that a bare npx invocation
// which does not name exactly one installable package spec stays
// Unresolvable: a --package list resolves several trees at once, a local
// path or git or file spec installs nothing into the digest-named
// directory, and an unknown flag may consume the next argument as its
// value. All of those would be wrong to fingerprint from a single-spec
// digest.
func TestResolveUnresolvableForNonNpxShapes(t *testing.T) {
	t.Setenv("NPM_CONFIG_CACHE", t.TempDir())
	tests := []struct {
		name string
		args []string
	}{
		{name: "no args", args: nil},
		{name: "flags only", args: []string{"-y"}},
		{name: "package flag", args: []string{"-y", "--package", "a-pkg", "--package", "b-pkg", "some-bin"}},
		{name: "local path spec", args: []string{"-y", "./my-server"}},
		{name: "absolute path spec", args: []string{"-y", "/opt/pkg", "serve"}},
		{name: "file url spec", args: []string{"-y", "file:../pkg", "serve"}},
		{name: "git spec", args: []string{"-y", "git+ssh://git@host/pkg.git", "serve"}},
		{name: "unknown flag with value", args: []string{"-y", "--registry", "http://x", "pkg", "serve"}},
		{name: "spec after separator", args: []string{"-y", "--", "pkg", "serve"}},
	}
	for _, tt := range tests {
		got := Resolve(pub_models.McpServer{Command: "npx", Args: tt.args})
		if got.Kind != Unresolvable {
			t.Errorf("%s: Resolve().Kind = %v, want Unresolvable", tt.name, got.Kind)
		}
		if got.Installed != nil {
			t.Errorf("%s: Resolve().Installed = %+v, want nil", tt.name, got.Installed)
		}
	}
}

// TestResolveUnpinnedFingerprintsInstalledManifest pins the dominant
// "npx -y <package>" shape end to end: the spec floats, yet the launcher
// keeps the installed tree where clai can read it, so the resolution is
// Unpinned with a manifest fingerprint attached. That fingerprint is what
// makes a warm cache hit self-verifying for this shape, which is the whole
// reason this package exists.
func TestResolveUnpinnedFingerprintsInstalledManifest(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv("NPM_CONFIG_CACHE", cacheDir)
	installDir := writeNpxInstall(t, cacheDir, "slivingdoc", "1.0.2")

	got := Resolve(pub_models.McpServer{Command: "npx", Args: []string{"-y", "slivingdoc", "serve"}})

	if got.Kind != Unpinned {
		t.Errorf("Kind = %v, want Unpinned", got.Kind)
	}
	if got.Installed == nil {
		t.Fatal("Installed is nil for an installed, resolvable package")
	}
	want := filepath.Join(installDir, "package-lock.json")
	if got.Installed.Path != want {
		t.Errorf("Installed.Path = %q, want the install dir's lockfile %q", got.Installed.Path, want)
	}
	if got.Installed.Size <= 0 {
		t.Errorf("Installed.Size = %d, want a positive size for an existing lockfile", got.Installed.Size)
	}
	if got.PinnedVersion != "" {
		t.Errorf("PinnedVersion = %q, want empty for an unpinned spec", got.PinnedVersion)
	}
}

// TestResolvePinnedReportsVersionWithoutInstalledTree pins that an exact
// spec resolves even when nothing is installed locally: the version is
// known from the config alone, so the identity needs no filesystem evidence
// and the server stays lazy on a cold cache.
func TestResolvePinnedReportsVersionWithoutInstalledTree(t *testing.T) {
	cacheDir := filepath.Join(t.TempDir(), "does-not-exist")
	t.Setenv("NPM_CONFIG_CACHE", cacheDir)
	got := Resolve(pub_models.McpServer{Command: "npx", Args: []string{"-y", "slivingdoc@1.0.2", "serve"}})
	if got.Kind != Pinned {
		t.Errorf("Kind = %v, want Pinned", got.Kind)
	}
	if got.PinnedVersion != "1.0.2" {
		t.Errorf("PinnedVersion = %q, want %q", got.PinnedVersion, "1.0.2")
	}
	if got.Installed != nil {
		t.Errorf("Installed = %+v, want nil: nothing is installed", got.Installed)
	}
}

// TestResolvePinnedDeltaChangesResolution pins that two different pinned
// versions are two different resolutions, which is what makes an upgrade of
// a pinned server a miss by construction instead of a wait out the freshness
// bound.
func TestResolvePinnedDeltaChangesResolution(t *testing.T) {
	cacheDir := filepath.Join(t.TempDir(), "does-not-exist")
	t.Setenv("NPM_CONFIG_CACHE", cacheDir)
	first := Resolve(pub_models.McpServer{Command: "npx", Args: []string{"-y", "slivingdoc@1.0.2", "serve"}})
	second := Resolve(pub_models.McpServer{Command: "npx", Args: []string{"-y", "slivingdoc@1.0.3", "serve"}})
	if first.PinnedVersion == second.PinnedVersion {
		t.Error("two different pinned versions produced the same PinnedVersion")
	}
	if first.Kind != Pinned || second.Kind != Pinned {
		t.Error("both resolutions must be Pinned for a pinned spec")
	}
}

// TestResolveUnpinnedManifestDeltaChangesResolution pins the other half: an
// upgrade of an unpinned package changes the manifest fingerprint, and that
// delta must reach the resolution. Without it the Installed field would be
// decorative.
func TestResolveUnpinnedManifestDeltaChangesResolution(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv("NPM_CONFIG_CACHE", cacheDir)
	writeNpxInstall(t, cacheDir, "slivingdoc", "1.0.2")

	before := Resolve(pub_models.McpServer{Command: "npx", Args: []string{"-y", "slivingdoc", "serve"}})
	if before.Installed == nil {
		t.Fatal("Installed is nil before the upgrade")
	}

	writeNpxInstall(t, cacheDir, "slivingdoc", "1.0.3")

	after := Resolve(pub_models.McpServer{Command: "npx", Args: []string{"-y", "slivingdoc", "serve"}})
	if after.Installed == nil {
		t.Fatal("Installed is nil after the upgrade")
	}
	if before.Installed.Size == after.Installed.Size && before.Installed.ModTime.Equal(after.Installed.ModTime) {
		t.Error("upgrading the installed package left the manifest fingerprint unchanged")
	}
	if before.Kind == Pinned || after.Kind == Pinned {
		t.Error("an unpinned spec must not be classified Pinned")
	}
}

// TestResolveUnresolvableCarriesNoEvidence pins that a server whose
// resolution failed contributes no fingerprint and no version at all. An
// absent component is what keeps its identity byte-identical to one built
// before this package existed, so every entry captured by an older clai
// still resolves and no stale entry is stranded.
func TestResolveUnresolvableCarriesNoEvidence(t *testing.T) {
	got := Resolve(pub_models.McpServer{Command: "docker", Args: []string{"run", "img:1"}})
	if got.Installed != nil || got.PinnedVersion != "" {
		t.Errorf("Unresolvable resolution carries evidence: %+v", got)
	}
	if got.Kind.String() != "unresolvable" {
		t.Errorf("Kind.String() = %q, want %q", got.Kind.String(), "unresolvable")
	}
	if Pinned.String() != "pinned" || Unpinned.String() != "unpinned" {
		t.Errorf("Kind strings = %q/%q, want pinned/unpinned", Pinned, Unpinned)
	}
}

// TestResolveRefusesInstallDirectoryNotNamingTheSpec pins the strictness
// rule: a digest-named directory whose own manifest does not list the spec
// is not evidence about this server, so it is refused. A fingerprint of the
// wrong tree would hold a stale tool list open past the freshness bound,
// which is strictly worse than no fingerprint at all.
func TestResolveRefusesInstallDirectoryNotNamingTheSpec(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv("NPM_CONFIG_CACHE", cacheDir)
	writeNpxInstallDeclaring(t, cacheDir, "slivingdoc", "other-pkg", "1.0.0")

	got := Resolve(pub_models.McpServer{Command: "npx", Args: []string{"-y", "slivingdoc", "serve"}})
	if got.Kind != Unresolvable {
		t.Errorf("Kind = %v, want Unresolvable for a directory that does not name the spec", got.Kind)
	}
	if got.Installed != nil {
		t.Errorf("Installed = %+v, want nil", got.Installed)
	}
}

// TestResolveUnresolvableWhenInstallDirectoryIsAbsent pins the ordinary
// miss: the spec is fine but the launcher has not installed it yet. There is
// no local evidence, so the resolution is Unresolvable and the caller falls
// back to connecting rather than trusting a guess.
func TestResolveUnresolvableWhenInstallDirectoryIsAbsent(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv("NPM_CONFIG_CACHE", cacheDir)
	got := Resolve(pub_models.McpServer{Command: "npx", Args: []string{"-y", "never-installed-pkg", "serve"}})
	if got.Kind != Unresolvable {
		t.Errorf("Kind = %v, want Unresolvable", got.Kind)
	}
	if got.Installed != nil {
		t.Errorf("Installed = %+v, want nil", got.Installed)
	}
}

// TestResolveAcceptsPinnedScopedPackage pins the spec shapes a pinned
// server actually uses in the wild, including a scoped package, so the
// pinned path is not limited to the unscoped simple case.
func TestResolveAcceptsPinnedScopedPackage(t *testing.T) {
	cacheDir := filepath.Join(t.TempDir(), "does-not-exist")
	t.Setenv("NPM_CONFIG_CACHE", cacheDir)
	got := Resolve(pub_models.McpServer{
		Command: "npx",
		Args:    []string{"-y", "@modelcontextprotocol/server-everything@2026.8.31"},
	})
	if got.Kind != Pinned {
		t.Errorf("Kind = %v, want Pinned", got.Kind)
	}
	if got.PinnedVersion != "2026.8.31" {
		t.Errorf("PinnedVersion = %q, want %q", got.PinnedVersion, "2026.8.31")
	}
}

// TestResolveRefusesLocallyShadowedPackage pins npx's own local tier: when a
// node_modules/.bin entry already provides the bin, npx runs that instead of
// the digest-named cache directory, so the cache directory is not evidence
// about this command line. Fingerprinting it anyway would hold a stale entry
// open for a program the command line does not identify.
func TestResolveRefusesLocallyShadowedPackage(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv("NPM_CONFIG_CACHE", cacheDir)
	writeNpxInstall(t, cacheDir, "slivingdoc", "1.0.2")

	cwd := t.TempDir()
	binDir := filepath.Join(cwd, "node_modules", ".bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir local bin: %v", err)
	}
	writeFakeBin(t, filepath.Join(binDir, "slivingdoc"))
	t.Chdir(cwd)

	got := Resolve(pub_models.McpServer{Command: "npx", Args: []string{"-y", "slivingdoc", "serve"}})
	if got.Kind != Unresolvable {
		t.Errorf("Kind = %v, want Unresolvable for a locally shadowed package", got.Kind)
	}
	if got.Installed != nil {
		t.Errorf("Installed = %+v, want nil", got.Installed)
	}
}

// TestResolveRefusesShadowFromAncestorNodeModules pins that the local tier
// walks up from the working directory, exactly as npx's walkUp does: a bin
// installed at a parent of the working directory still shadows the cache.
func TestResolveRefusesShadowFromAncestorNodeModules(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv("NPM_CONFIG_CACHE", cacheDir)
	writeNpxInstall(t, cacheDir, "slivingdoc", "1.0.2")

	root := t.TempDir()
	binDir := filepath.Join(root, "node_modules", ".bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir local bin: %v", err)
	}
	writeFakeBin(t, filepath.Join(binDir, "slivingdoc"))
	sub := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("mkdir subdir: %v", err)
	}
	t.Chdir(sub)

	got := Resolve(pub_models.McpServer{Command: "npx", Args: []string{"-y", "slivingdoc", "serve"}})
	if got.Kind != Unresolvable {
		t.Errorf("Kind = %v, want Unresolvable for an ancestor-shadowed package", got.Kind)
	}
}

// TestResolveRefusesCwdPackageBin pins npx's first tier: a working directory
// whose own package.json declares a bin by the spec's name is the package
// npx installs and runs, not the cache directory.
func TestResolveRefusesCwdPackageBin(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv("NPM_CONFIG_CACHE", cacheDir)
	writeNpxInstall(t, cacheDir, "slivingdoc", "1.0.2")

	cwd := t.TempDir()
	manifest := `{"name":"local","bin":{"slivingdoc":"cli.js"}}`
	if err := os.WriteFile(filepath.Join(cwd, "package.json"), []byte(manifest), 0o644); err != nil {
		t.Fatalf("write cwd package.json: %v", err)
	}
	t.Chdir(cwd)

	got := Resolve(pub_models.McpServer{Command: "npx", Args: []string{"-y", "slivingdoc", "serve"}})
	if got.Kind != Unresolvable {
		t.Errorf("Kind = %v, want Unresolvable for a cwd package bin", got.Kind)
	}
}

// TestResolveRefusesGloballyShadowedPackage pins npx's global bin tier: a
// package installed into npm's global bin runs instead of the cache.
func TestResolveRefusesGloballyShadowedPackage(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv("NPM_CONFIG_CACHE", cacheDir)
	writeNpxInstall(t, cacheDir, "slivingdoc", "1.0.2")

	prefix := t.TempDir()
	globalBin := filepath.Join(prefix, "bin")
	if err := os.MkdirAll(globalBin, 0o755); err != nil {
		t.Fatalf("mkdir global bin: %v", err)
	}
	writeFakeBin(t, filepath.Join(globalBin, "slivingdoc"))
	t.Setenv("NPM_CONFIG_PREFIX", prefix)

	got := Resolve(pub_models.McpServer{Command: "npx", Args: []string{"-y", "slivingdoc", "serve"}})
	if got.Kind != Unresolvable {
		t.Errorf("Kind = %v, want Unresolvable for a globally shadowed package", got.Kind)
	}
}

// TestResolveRefusesGlobalBinDerivedFromNode pins the default global bin,
// which has no prefix override: npm's global bin is the node executable's own
// directory. A shadow there must be caught without any env override.
func TestResolveRefusesGlobalBinDerivedFromNode(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv("NPM_CONFIG_CACHE", cacheDir)
	writeNpxInstall(t, cacheDir, "slivingdoc", "1.0.2")

	binDir := t.TempDir()
	writeFakeBin(t, filepath.Join(binDir, "node"))
	writeFakeBin(t, filepath.Join(binDir, "npx"))
	writeFakeBin(t, filepath.Join(binDir, "slivingdoc"))
	t.Setenv("NPM_CONFIG_PREFIX", "")
	t.Setenv("npm_config_prefix", "")
	t.Setenv("PATH", binDir)

	got := Resolve(pub_models.McpServer{Command: "npx", Args: []string{"-y", "slivingdoc", "serve"}})
	if got.Kind != Unresolvable {
		t.Errorf("Kind = %v, want Unresolvable for a node-derived global bin shadow", got.Kind)
	}
}

// TestResolveIgnoresUnrelatedPathBinary pins the asymmetry that keeps the
// fingerprint useful: a same-named executable on PATH that is not npm's
// global bin is not what npx would run, so it must not suppress the
// fingerprint. A Go-installed binary sharing the package's name is the
// motivating shape.
func TestResolveIgnoresUnrelatedPathBinary(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv("NPM_CONFIG_CACHE", cacheDir)
	writeNpxInstall(t, cacheDir, "slivingdoc", "1.0.2")

	other := t.TempDir()
	writeFakeBin(t, filepath.Join(other, "slivingdoc"))
	t.Setenv("NPM_CONFIG_PREFIX", "")
	t.Setenv("npm_config_prefix", "")
	t.Setenv("PATH", other+string(os.PathListSeparator)+os.Getenv("PATH"))

	got := Resolve(pub_models.McpServer{Command: "npx", Args: []string{"-y", "slivingdoc", "serve"}})
	if got.Kind != Unpinned {
		t.Errorf("Kind = %v, want Unpinned: a PATH binary outside npm's global bin is not a shadow", got.Kind)
	}
}

// TestResolveVersionedSpecIgnoresBareLocalBin pins the exact-spec rule npx
// enforces: npx looks up node_modules/.bin/<spec as written>, so a bare
// local bin does not shadow "slivingdoc@latest"; the cache directory is
// still what runs and stays evidence.
func TestResolveVersionedSpecIgnoresBareLocalBin(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv("NPM_CONFIG_CACHE", cacheDir)
	writeNpxInstall(t, cacheDir, "slivingdoc@latest", "1.0.2")

	cwd := t.TempDir()
	binDir := filepath.Join(cwd, "node_modules", ".bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir local bin: %v", err)
	}
	writeFakeBin(t, filepath.Join(binDir, "slivingdoc"))
	t.Chdir(cwd)

	got := Resolve(pub_models.McpServer{Command: "npx", Args: []string{"-y", "slivingdoc@latest", "serve"}})
	if got.Kind != Unpinned {
		t.Errorf("Kind = %v, want Unpinned: a bare local bin does not shadow a versioned spec", got.Kind)
	}
}

// writeFakeBin writes an executable stub at path, which is all a shadow check
// reads: the file's existence is the evidence, its body is never run.
func writeFakeBin(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write fake bin %q: %v", path, err)
	}
}

// writeNpxInstall materialises the two files libnpmexec leaves behind that
// together identify an npx install: the root manifest naming the package and
// the lockfile pinning the resolved version. It returns the install
// directory it wrote.
func writeNpxInstall(t *testing.T, cacheDir, spec, version string) string {
	t.Helper()
	return writeNpxInstallDeclaring(t, cacheDir, spec, spec, version)
}

// writeNpxInstallDeclaring writes an install directory named by the digest
// of digestSpec while declaring declaredSpec in its manifest, so a test can
// reproduce a digest-named directory that does not hold what its name
// implies.
func writeNpxInstallDeclaring(t *testing.T, cacheDir, digestSpec, declaredSpec, version string) string {
	t.Helper()
	dir := NpxInstallDir([]string{digestSpec}, cacheDir)
	if dir == "" {
		t.Fatalf("NpxInstallDir empty for spec %q under cache %q", digestSpec, cacheDir)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %q: %v", dir, err)
	}
	root := `{"dependencies":{"` + declaredSpec + `":"^` + version + `"},"_npx":{"packages":["` + declaredSpec + `@latest"]}}`
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(root), 0o644); err != nil {
		t.Fatalf("write npx root package.json: %v", err)
	}
	lock := `{"name":"` + filepath.Base(dir) + `","lockfileVersion":3,"packages":{"":{},"node_modules/` + declaredSpec + `":{"version":"` + version + `"}}}`
	if err := os.WriteFile(filepath.Join(dir, "package-lock.json"), []byte(lock), 0o644); err != nil {
		t.Fatalf("write npx package-lock.json: %v", err)
	}
	return dir
}

// TestKnownClassifiesEveryOutcomeButUnresolvable pins the posture predicate
// Resolve's callers branch on: only Unresolvable is the absence of evidence.
func TestKnownClassifiesEveryOutcomeButUnresolvable(t *testing.T) {
	tests := []struct {
		kind Kind
		want bool
	}{
		{kind: Exact, want: true},
		{kind: Pinned, want: true},
		{kind: Unpinned, want: true},
		{kind: Unresolvable, want: false},
	}
	for _, tt := range tests {
		if got := (Resolution{Kind: tt.kind}).Known(); got != tt.want {
			t.Errorf("Resolution{Kind: %v}.Known() = %v, want %v", tt.kind, got, tt.want)
		}
	}
}

// TestRequiresEagerConnectPosture pins which shapes force a setup-time
// connect. An unmodelled command line is eager; an endpoint, a server with no
// command, and a command line clai can identify are left lazy.
func TestRequiresEagerConnectPosture(t *testing.T) {
	t.Setenv("NPM_CONFIG_CACHE", t.TempDir())
	exact := filepath.Join(t.TempDir(), "server")
	writeFakeBin(t, exact)
	tests := []struct {
		name   string
		server pub_models.McpServer
		want   bool
	}{
		{name: "endpoint", server: pub_models.McpServer{Url: "https://mcp.example.com/mcp"}, want: false},
		{name: "endpoint with a command", server: pub_models.McpServer{Command: "npx", Url: "https://mcp.example.com/mcp"}, want: false},
		{name: "no command", server: pub_models.McpServer{}, want: false},
		{name: "unmodelled command", server: pub_models.McpServer{Command: "/opt/custom/server"}, want: true},
		{name: "unresolvable launcher", server: pub_models.McpServer{Command: "uvx", Args: []string{"serve"}}, want: true},
		{name: "exact binary", server: pub_models.McpServer{Command: exact}, want: false},
		{name: "interpreter given a script", server: pub_models.McpServer{Command: "sh", Args: []string{exact}}, want: false},
	}
	for _, tt := range tests {
		if got := RequiresEagerConnect(tt.server); got != tt.want {
			t.Errorf("%s: RequiresEagerConnect() = %v, want %v", tt.name, got, tt.want)
		}
	}
}

// TestExecutableReportsAbsenceAndPresence pins the evidence helper the
// identity delegates to: a resolved executable carries its path and delta, and
// every absence is false rather than an error.
func TestExecutableReportsAbsenceAndPresence(t *testing.T) {
	file := filepath.Join(t.TempDir(), "server")
	writeFakeBin(t, file)
	if _, _, ok := Executable(""); ok {
		t.Error("Executable(\"\") = true, want false")
	}
	if _, _, ok := Executable("definitely-not-a-real-binary-xyz"); ok {
		t.Error("Executable(unresolvable) = true, want false")
	}
	if _, _, ok := Executable(t.TempDir()); ok {
		t.Error("Executable(directory) = true, want false")
	}
	path, fp, ok := Executable(file)
	if !ok {
		t.Fatalf("Executable(%q) = false, want true", file)
	}
	if path != file {
		t.Errorf("Executable path = %q, want %q", path, file)
	}
	if fp.Size <= 0 {
		t.Errorf("Executable size = %d, want the file's size", fp.Size)
	}
}

// TestScriptArgReturnsFirstRegularFile pins that a script argument is
// fingerprinted only when it names a regular file: blanks, misses and
// directories are skipped, and the first hit wins.
func TestScriptArgReturnsFirstRegularFile(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "server.js")
	if err := os.WriteFile(file, []byte("console.log(1)\n"), 0o644); err != nil {
		t.Fatalf("write script: %v", err)
	}
	if _, _, ok := ScriptArg(nil); ok {
		t.Error("ScriptArg(nil) = true, want false")
	}
	if _, _, ok := ScriptArg([]string{"", "  ", filepath.Join(dir, "missing")}); ok {
		t.Error("ScriptArg(misses) = true, want false")
	}
	if _, _, ok := ScriptArg([]string{dir}); ok {
		t.Error("ScriptArg(directory) = true, want false")
	}
	got, _, ok := ScriptArg([]string{"", dir, file})
	if !ok {
		t.Fatal("ScriptArg() did not find the script")
	}
	if got != file {
		t.Errorf("ScriptArg() = %q, want %q", got, file)
	}
}

// TestFingerprintsFileReportsAbsenceAndPresence pins the envfile contract: the
// delta of a regular file, and the canonical absent marker for every other
// shape, never an error.
func TestFingerprintsFileReportsAbsenceAndPresence(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "server.env")
	contents := "KEY=value\n"
	if err := os.WriteFile(file, []byte(contents), 0o644); err != nil {
		t.Fatalf("write envfile: %v", err)
	}
	if _, ok := FingerprintsFile(""); ok {
		t.Error("FingerprintsFile(\"\") = true, want false")
	}
	if _, ok := FingerprintsFile(filepath.Join(dir, "missing")); ok {
		t.Error("FingerprintsFile(missing) = true, want false")
	}
	if _, ok := FingerprintsFile(dir); ok {
		t.Error("FingerprintsFile(directory) = true, want false")
	}
	fp, ok := FingerprintsFile(file)
	if !ok {
		t.Fatalf("FingerprintsFile(%q) = false, want true", file)
	}
	if fp.Size != int64(len(contents)) {
		t.Errorf("FingerprintsFile size = %d, want %d", fp.Size, len(contents))
	}
}

// TestNpmCacheRootHonoursOverrideThenHome pins the resolution order npm itself
// uses: the cache override wins, then the home default, and neither present is
// the empty path a caller must treat as no evidence rather than a guess.
func TestNpmCacheRootHonoursOverrideThenHome(t *testing.T) {
	t.Setenv("NPM_CONFIG_CACHE", "")
	t.Setenv("npm_config_cache", "")
	t.Setenv("HOME", "/home/example")
	if got := npmCacheRoot(); got != "/home/example/.npm" {
		t.Errorf("npmCacheRoot() = %q, want the home default", got)
	}
	t.Setenv("NPM_CONFIG_CACHE", "/tmp/cache-override")
	if got := npmCacheRoot(); got != "/tmp/cache-override" {
		t.Errorf("npmCacheRoot() = %q, want the upper-case override", got)
	}
	t.Setenv("NPM_CONFIG_CACHE", "")
	t.Setenv("npm_config_cache", "/tmp/lower-override")
	if got := npmCacheRoot(); got != "/tmp/lower-override" {
		t.Errorf("npmCacheRoot() = %q, want the lower-case override", got)
	}
	t.Setenv("npm_config_cache", "")
	t.Setenv("HOME", "")
	if got := npmCacheRoot(); got != "" {
		t.Errorf("npmCacheRoot() = %q, want empty with no override and no home", got)
	}
}

// TestGenericLauncherRejectsBlankAndNonLaunchers pins that the modelled set is
// closed: a blank command, an unresolvable one, and a real program outside the
// set are all false, so none of them is credited as a launcher.
func TestGenericLauncherRejectsBlankAndNonLaunchers(t *testing.T) {
	if GenericLauncher("") {
		t.Error("GenericLauncher(\"\") = true, want false")
	}
	if GenericLauncher("definitely-not-a-real-binary-xyz") {
		t.Error("GenericLauncher(unresolvable) = true, want false")
	}
	if GenericLauncher("sh") {
		t.Error("GenericLauncher(\"sh\") = true, want false: a shell is outside the modelled set")
	}
}

// TestCwdPackageProvidesBinRecognisesBothDeclarations pins npx's first tier in
// both package.json forms and against the shapes that must not shadow: an
// unrelated name, a missing bin, and a malformed manifest.
func TestCwdPackageProvidesBinRecognisesBothDeclarations(t *testing.T) {
	t.Chdir(t.TempDir())
	write := func(body string) {
		t.Helper()
		if err := os.WriteFile("package.json", []byte(body), 0o644); err != nil {
			t.Fatalf("write package.json: %v", err)
		}
	}

	write(`{"name":"local","bin":{"slivingdoc":"cli.js"}}`)
	if !cwdPackageProvidesBin("slivingdoc") {
		t.Error("named bin declaration was not recognised")
	}
	if cwdPackageProvidesBin("other") {
		t.Error("an absent bin name was reported present")
	}

	write(`{"name":"slivingdoc","bin":"cli.js"}`)
	if !cwdPackageProvidesBin("slivingdoc") {
		t.Error("single-string bin declaration was not matched to the package's own name")
	}
	if cwdPackageProvidesBin("other") {
		t.Error("single-string bin declaration matched a different spec")
	}

	write(`{"name":"local","bin":"cli.js"}`)
	if cwdPackageProvidesBin("other") {
		t.Error("single-string bin declaration matched a spec that is not the package name")
	}

	write(`{"name":"local"}`)
	if cwdPackageProvidesBin("local") {
		t.Error("a package.json with no bin was treated as a shadow")
	}

	write(`not json`)
	if cwdPackageProvidesBin("local") {
		t.Error("a malformed package.json was treated as a shadow")
	}
}

// TestGlobalBinProvidesFalseWithoutNode pins the no-global-bin arm: with no
// prefix override and no node on PATH there is no global bin, so nothing can
// shadow the cache from there.
func TestGlobalBinProvidesFalseWithoutNode(t *testing.T) {
	t.Setenv("NPM_CONFIG_PREFIX", "")
	t.Setenv("npm_config_prefix", "")
	t.Setenv("PATH", t.TempDir())
	if globalBinProvides("slivingdoc") {
		t.Error("globalBinProvides = true with no prefix override and no node on PATH")
	}
}
