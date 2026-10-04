// Package launcher resolves what a command-based MCP server's command line
// will actually run, using only local evidence and without starting anything.
//
// The question it answers is the one a lazy server cannot answer at setup: a
// bare package runner names a package, not a program, so the version that
// will execute is not visible in the config. Resolve classifies a server's
// command line into exactly one of four outcomes, and the caller picks its
// startup posture from that classification.
//
// An unresolvable command line is not a failure: no local evidence exists,
// so the outcome carries none, and the caller connects to observe the tool
// list for real. That default is what makes an unmodelled launcher — a new
// package runner, or one clai has never heard of — safe by default rather
// than by someone remembering to add it to a table.
package launcher

import (
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// Kind classifies how well a command line's resolved content is known.
type Kind string

const (
	// Exact means the thing that will run is itself a local file clai
	// fingerprinted: a directly executed binary, or an interpreter given a
	// script path. Its identity carries that file's size and modification
	// time, so an upgrade is a miss by construction and no further evidence
	// is needed.
	Exact Kind = "exact"

	// Pinned means the command line names one exact package version, so the
	// content is knowable from the config alone with no filesystem evidence
	// at all. The version is the identity component that changes when the
	// pin moves.
	Pinned Kind = "pinned"

	// Unpinned means the package spec floats, but the launcher's install
	// directory is discoverable and names the spec, so the installed
	// manifest is readable and becomes the identity component. This is the
	// "npx -y <package>" shape, which is most command-based servers in
	// practice.
	Unpinned Kind = "unpinned"

	// Unresolvable means no local evidence of the content that will run
	// exists: an unmodelled launcher, a launcher form clai cannot resolve
	// without guessing, a package the launcher has not installed, or an
	// endpoint-based server, which has no local content to fingerprint at
	// all. RequiresEagerConnect decides what to do about it.
	Unresolvable Kind = "unresolvable"
)

// String is the Kind's stable textual form, used in identity encodings and
// in any operator-facing message, so a stored entry stays readable.
func (k Kind) String() string { return string(k) }

// Manifest is the size-and-modification-time evidence for the file a launcher
// installed, plus the path it was found at: two installs of different
// packages can agree on both size and modification time, never on path.
type Manifest struct {
	Path    string    `json:"path"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"mtime"`
}

// Fingerprint is the delta evidence for any local file a command line names,
// used by the schema cache's identity components. It carries no path: the
// caller already knows which component the path belongs to.
type Fingerprint struct {
	Size    int64     `json:"size"`
	ModTime time.Time `json:"mtime"`
}

// Resolution is the outcome of resolving one server's command line.
//
// Spec, PinnedVersion and Installed are populated only when they carry
// evidence: a Pinned resolution names its version, an Unpinned one its
// installed manifest, and both an Exact and an Unresolvable one leave every
// field empty, so an Unresolvable resolution changes no identity component
// and cannot strand an entry captured before this package existed.
type Resolution struct {
	Kind          Kind
	Spec          string
	PinnedVersion string
	Installed     *Manifest
}

// Known reports whether clai holds evidence of what will run, which is every
// outcome but Unresolvable. A server whose resolution is known may be lazy
// on a cache hit; one that is not must connect to observe.
func (r Resolution) Known() bool { return r.Kind != Unresolvable }

// RequiresEagerConnect reports whether server's command line must be
// connected during setup rather than deferred to its first tool call.
//
// It is true only for a command-based server whose resolved content clai
// cannot see. Two shapes are deliberately excluded. An endpoint-based server
// has no command to resolve and no local content, and it was already lazy by
// default with the freshness bound governing its entry; forcing it eager would
// change a working posture for no gain. And an explicitly configured startup
// mode is not overridden, because an operator who asked for one has accepted
// the trade, exactly as for every other startup parameter.
func RequiresEagerConnect(server pub_models.McpServer) bool {
	if server.Command == "" || server.Url != "" {
		return false
	}
	return !Resolve(server).Known()
}

// Resolve classifies server's command line. It never fails and never starts
// anything: an unknown or unreadable input is the Unresolvable outcome, the
// same as any other absence of evidence.
func Resolve(server pub_models.McpServer) Resolution {
	if server.Command == "" {
		return Resolution{Kind: Unresolvable}
	}
	if GenericLauncher(server.Command) {
		if filepath.Base(server.Command) == "npx" {
			return resolveNpx(server.Args)
		}
		// A launcher clai does not resolve still gets its script argument
		// credited: "node /path/server.js" names the program that runs, so
		// the local file is exact evidence whatever else the launcher is.
		if _, _, ok := ScriptArg(server.Args); ok {
			return Resolution{Kind: Exact}
		}
		return Resolution{Kind: Unresolvable}
	}
	if _, _, ok := ScriptArg(server.Args); ok {
		return Resolution{Kind: Exact}
	}
	if _, _, ok := Executable(server.Command); ok {
		return Resolution{Kind: Exact}
	}
	return Resolution{Kind: Unresolvable}
}

// GenericLauncher reports whether command is an interpreter or package
// runner: a program whose own path, size and modification time say nothing
// about the content it will execute. The launcher is resolved only to the
// extent this package models it, so an unmodelled one is not Exact by virtue
// of its own binary being readable. Matched against the command's basename,
// so an alternate install location is still recognised.
func GenericLauncher(command string) bool {
	if strings.TrimSpace(command) == "" {
		return false
	}
	if _, _, ok := Executable(command); !ok {
		return false
	}
	return genericLaunchers[filepath.Base(command)]
}

// genericLaunchers names the interpreters and package runners whose
// executable is not the program that runs.
var genericLaunchers = map[string]bool{
	"node": true, "npx": true, "npm": true,
	"uv": true, "uvx": true, "pipx": true,
	"python": true, "python3": true,
	"docker": true, "bunx": true, "deno": true,
}

// exactVersion is a bare semantic version with an optional prerelease. Build
// metadata is excluded deliberately: a "+" build suffix does not change the
// package's code, and a spec carrying one is far more likely to be a range or
// an alias than a pinned release.
var exactVersion = regexp.MustCompile(`^=?\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?$`)

// SpecIsPinned reports whether spec names one exact package version, which is
// the only spec form whose resolved content a reader can state without
// consulting a registry or a cache directory. Everything else — a bare name,
// a dist-tag, a partial version, a range, a path or VCS spec — floats, and a
// float must never be pinned, since pinning to content nobody can predict is
// worse than admitting the version is unknown.
func SpecIsPinned(spec string) bool {
	version, ok := specVersion(spec)
	return ok && exactVersion.MatchString(version)
}

// specVersion splits a spec's version selector off, reporting false for a
// spec that carries none. The split is at the first "@" that is not the
// scope marker a leading "@" opens, so a scoped package resolves correctly
// while an aliased spec ("pkg@npm:other@1.0.0") keeps its whole selector and
// is therefore not mistaken for a pinned version.
func specVersion(spec string) (string, bool) {
	from := 0
	if strings.HasPrefix(spec, "@") {
		from = 1
	}
	at := strings.Index(spec[from:], "@")
	if at <= 0 {
		return "", false
	}
	at += from
	if at == len(spec)-1 {
		return "", false
	}
	return spec[at+1:], true
}

// specName strips a spec's version selector, leaving the package name an
// install directory's own manifest is indexed by. Both are needed: the name
// identifies the package, the selector identifies the version.
func specName(spec string) string {
	if _, ok := specVersion(spec); !ok {
		return spec
	}
	at := strings.LastIndex(spec, "@")
	if at <= 0 {
		return spec
	}
	return spec[:at]
}

// npxFlagsWithoutValue names npx's boolean flags, the only arguments this
// package skips while looking for the one package spec. Every other flag is
// unmodelled: npx accepts many, any of which may consume the following
// argument as its value, so an unknown flag makes the command line
// unresolvable rather than merely unusual.
var npxFlagsWithoutValue = map[string]bool{
	"-y": true, "--yes": true,
	"-q": true, "--quiet": true,
	"--ignore-existing": true,
	"--no":              true,
	"--offline":         true,
	"--prefer-offline":  true,
	"--no-install":      true,
}

// npxPackageSpec extracts the package an npx command line installs, reporting
// false when the command line does not name exactly one installable spec. The
// first positional argument is that package and the one whose bin is run,
// which is the shape every "npx -y <package> <server args>" config uses; every
// later positional is an argument to it, never a second package.
//
// A flag this package does not know is refused rather than skipped: any of
// npm's flags may consume the following argument as its value, and an argument
// read wrongly would fingerprint the wrong tree.
func npxPackageSpec(args []string) (string, bool) {
	for _, arg := range args {
		arg = strings.TrimSpace(arg)
		if arg == "" {
			continue
		}
		if arg == "--" {
			// Everything after the separator is the command to run rather
			// than a spec to install.
			return "", false
		}
		if strings.HasPrefix(arg, "-") {
			if npxFlagsWithoutValue[arg] {
				continue
			}
			return "", false
		}
		if !InstallableSpec(arg) {
			return "", false
		}
		return arg, true
	}
	return "", false
}

// nonInstallableSpecPrefixes names the spec forms that name something other
// than a registry package, so npx installs them by a path npx itself
// resolves and never into the digest-named install directory.
var nonInstallableSpecPrefixes = []string{
	"file:", "link:", "portal:", "npm:", "workspace:", "git:", "git+",
	"github:", "gitlab:", "bitbucket:",
}

// InstallableSpec reports whether spec names a registry package, which is the
// only form npx installs into the digest-named install directory. A local
// path, a URL or a VCS alias resolves outside that directory entirely, so a
// fingerprint taken from it would describe a tree nothing will run.
func InstallableSpec(spec string) bool {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return false
	}
	if strings.HasPrefix(spec, ".") || strings.HasPrefix(spec, "/") || strings.HasPrefix(spec, "~") {
		return false
	}
	if strings.Contains(spec, "://") {
		return false
	}
	for _, prefix := range nonInstallableSpecPrefixes {
		if strings.HasPrefix(spec, prefix) {
			return false
		}
	}
	if strings.HasPrefix(spec, "@") && !strings.Contains(spec[1:], "/") {
		return false
	}
	return true
}

// resolveNpx resolves an npx command line against npm's own install layout.
func resolveNpx(args []string) Resolution {
	spec, ok := npxPackageSpec(args)
	if !ok {
		return Resolution{Kind: Unresolvable}
	}
	if SpecIsPinned(spec) {
		version, _ := specVersion(spec)
		return Resolution{
			Kind:          Pinned,
			Spec:          specName(spec),
			PinnedVersion: strings.TrimPrefix(version, "="),
		}
	}
	name := specName(spec)
	if shadowedByName(spec) {
		return Resolution{Kind: Unresolvable, Spec: name}
	}
	dir := NpxInstallDir([]string{spec}, npmCacheRoot())
	if dir == "" || !installDirNames(dir, name) {
		return Resolution{Kind: Unresolvable, Spec: name}
	}
	manifest, ok := installedManifest(dir, name)
	if !ok {
		return Resolution{Kind: Unresolvable, Spec: name}
	}
	return Resolution{Kind: Unpinned, Spec: name, Installed: &manifest}
}

// NpxInstallDir reports the directory npx installs specs into, or the empty
// string when it cannot be determined: no specs, a blank spec, or no
// resolvable cache root. The directory name is the first 16 hex characters
// of the sha512 of the specs joined by newlines in sorted order, which is
// libnpmexec's own construction — the digest names the directory, and the
// directory is where the installed package lives.
//
// The layout this reproduces is npm's, not clai's, so an npm change that
// renames the directory makes this return a path that does not exist, which
// callers treat as absent evidence. That failure mode is the safe direction:
// a miss costs one connect, while a wrong path would fingerprint the wrong
// tree and hold a stale entry open.
func NpxInstallDir(specs []string, cacheRoot string) string {
	for _, s := range specs {
		if strings.TrimSpace(s) == "" {
			// A blank member makes libnpmexec's own input unknowable, so the
			// digest it would compute is not reproducible here.
			return ""
		}
	}
	if len(specs) == 0 || strings.TrimSpace(cacheRoot) == "" {
		return ""
	}
	cleaned := slices.Clone(specs)
	slices.Sort(cleaned)
	sum := sha512.Sum512([]byte(strings.Join(cleaned, "\n")))
	return filepath.Join(cacheRoot, "_npx", hex.EncodeToString(sum[:])[:16])
}

// npmCacheRoot resolves the cache directory npx installs into, honouring the
// environment override npm itself reads before its default. It returns the
// empty string when neither the override nor a home directory is available.
func npmCacheRoot() string {
	for _, key := range []string{"NPM_CONFIG_CACHE", "npm_config_cache"} {
		if v := strings.TrimSpace(os.Getenv(key)); v != "" {
			return v
		}
	}
	home := strings.TrimSpace(os.Getenv("HOME"))
	if home == "" {
		return ""
	}
	return filepath.Join(home, ".npm")
}

// shadowedByName reports whether npx would run a bin the local tree already
// provides instead of the digest-named cache directory, in which case that
// directory is not evidence about this command line. npx checks three tiers
// before the cache (libnpmexec/lib/index.js): the working directory package's
// own bin, a node_modules/.bin entry at or above the working directory, and
// npm's global bin. The bin name is the spec exactly as written, so a
// versioned spec ("pkg@latest") is never shadowed by a bare local bin — the
// cache is still what runs, and the fingerprint stays honest.
func shadowedByName(spec string) bool {
	if strings.TrimSpace(spec) == "" {
		return false
	}
	if cwdPackageProvidesBin(spec) {
		return true
	}
	if localNodeModulesBin(spec) {
		return true
	}
	return globalBinProvides(spec)
}

// cwdPackageProvidesBin reproduces npx's first tier: a package.json in the
// working directory that declares a bin by the spec's name is the package npx
// installs and runs, not whatever the cache directory holds.
func cwdPackageProvidesBin(spec string) bool {
	data, err := os.ReadFile("package.json")
	if err != nil {
		return false
	}
	var pkg struct {
		Name string          `json:"name"`
		Bin  json.RawMessage `json:"bin"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil || len(pkg.Bin) == 0 {
		return false
	}
	var named map[string]string
	if err := json.Unmarshal(pkg.Bin, &named); err == nil {
		_, ok := named[spec]
		return ok
	}
	var single string
	if err := json.Unmarshal(pkg.Bin, &single); err == nil {
		return pkg.Name == spec
	}
	return false
}

// localNodeModulesBin reproduces npx's second tier: it walks up from the
// working directory checking <dir>/node_modules/.bin/<spec>, the same walkUp
// npx performs. A working directory it cannot read is treated as shadowed, so
// uncertainty refuses the fingerprint rather than guessing one.
func localNodeModulesBin(spec string) bool {
	dir, err := os.Getwd()
	if err != nil {
		return true
	}
	for {
		if regularFile(filepath.Join(dir, "node_modules", ".bin", spec)) {
			return true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return false
		}
		dir = parent
	}
}

// globalBinProvides reproduces npx's third tier: npm's global bin, which is
// the configured prefix's bin directory or, absent an override, the node
// executable's own directory. Absent node there is no global bin to speak of,
// since npx could not run either.
func globalBinProvides(spec string) bool {
	bin := npmGlobalBin()
	if bin == "" {
		return false
	}
	return regularFile(filepath.Join(bin, spec))
}

// npmGlobalBin resolves npm's global bin directory. The prefix environment
// override wins, matching npm's own precedence; otherwise the bin directory is
// the directory the node executable lives in, which is where npm installs
// globals by default.
func npmGlobalBin() string {
	for _, key := range []string{"NPM_CONFIG_PREFIX", "npm_config_prefix"} {
		if v := strings.TrimSpace(os.Getenv(key)); v != "" {
			return filepath.Join(v, "bin")
		}
	}
	node, err := exec.LookPath("node")
	if err != nil {
		return ""
	}
	return filepath.Dir(node)
}

// regularFile reports whether path names an existing regular file, the shape
// npx's own fileExists and localFileExists accept.
func regularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// installDirNames reports whether dir's own manifest declares spec among the
// packages npx installed there. This is the cross-check that keeps a
// fingerprint honest: the directory name is a digest of the spec, so a
// directory whose contents do not match its name is not evidence about that
// spec, and fingerprinting it would keep a stale tool list alive past the
// freshness bound.
func installDirNames(dir, spec string) bool {
	data, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		return false
	}
	var manifest struct {
		Npx struct {
			Packages []string `json:"packages"`
		} `json:"_npx"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return false
	}
	for _, installed := range manifest.Npx.Packages {
		if installed == spec || strings.HasPrefix(installed, spec+"@") {
			return true
		}
	}
	return false
}

// installedManifest fingerprints the file an npx install directory pins its
// resolved version in: the lockfile when present, since npm rewrites it on an
// upgrade and it carries the version, the resolved URL and the tarball
// integrity, and the installed package's own manifest otherwise.
func installedManifest(dir, spec string) (Manifest, bool) {
	for _, name := range []string{
		"package-lock.json",
		filepath.Join("node_modules", filepath.FromSlash(spec), "package.json"),
	} {
		path := filepath.Join(dir, name)
		info, err := os.Stat(path)
		if err != nil || info.IsDir() {
			continue
		}
		return Manifest{Path: path, Size: info.Size(), ModTime: info.ModTime()}, true
	}
	return Manifest{}, false
}

// Executable resolves command through PATH and fingerprints the file it
// names, reporting false when it resolves to nothing or to a directory. The
// path travels with the evidence because two commands can coincide on size
// and modification time but never on path.
func Executable(command string) (string, Fingerprint, bool) {
	if strings.TrimSpace(command) == "" {
		return "", Fingerprint{}, false
	}
	path, err := exec.LookPath(command)
	if err != nil {
		return "", Fingerprint{}, false
	}
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return "", Fingerprint{}, false
	}
	return path, Fingerprint{Size: info.Size(), ModTime: info.ModTime()}, true
}

// ScriptArg fingerprints the first argument that names an existing regular
// file, which for an interpreter-launched server is the server script itself:
// "node /path/server.js". It is false for a package name, which is not a
// path, and for any argument npx or another runner will interpret itself.
func ScriptArg(args []string) (string, Fingerprint, bool) {
	for _, arg := range args {
		if strings.TrimSpace(arg) == "" {
			continue
		}
		info, err := os.Stat(arg)
		if err != nil || info.IsDir() {
			continue
		}
		return arg, Fingerprint{Size: info.Size(), ModTime: info.ModTime()}, true
	}
	return "", Fingerprint{}, false
}

// FingerprintsFile reports the delta evidence for the named file, which is
// the shape an envfile's freshness rides since its contents are never
// digested. An absent or unreadable path is false, the canonical absent
// marker, never an error.
func FingerprintsFile(path string) (Fingerprint, bool) {
	if strings.TrimSpace(path) == "" {
		return Fingerprint{}, false
	}
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return Fingerprint{}, false
	}
	return Fingerprint{Size: info.Size(), ModTime: info.ModTime()}, true
}
