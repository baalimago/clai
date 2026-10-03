package text

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/baalimago/clai/internal/debugflags"
	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/tools"
	"github.com/baalimago/clai/internal/tools/mcp"
	"github.com/baalimago/clai/internal/tools/mcp/launcher"
	"github.com/baalimago/clai/internal/tools/mcp/mcpauth"
	"github.com/baalimago/clai/internal/tools/mcp/schemacache"
	"github.com/baalimago/clai/internal/tools/mcp/serverconfig"
	"github.com/baalimago/clai/internal/utils"
	"github.com/baalimago/clai/pkg/claierr"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
	"github.com/baalimago/go_away_boilerplate/pkg/debug"
	"github.com/baalimago/go_away_boilerplate/pkg/misc"

	pub_models "github.com/baalimago/clai/pkg/text/models"
	pkgtools "github.com/baalimago/clai/pkg/tools"
)

// filterMcpServersByProfile filters MCP server files based on whether their tools are needed by the profile
func filterMcpServersByProfile(mcpServerPaths []string, userConf Configurations) []string {
	// If no specific tools are configured, load all servers (existing behavior)
	if len(userConf.RequestedToolGlobs) == 0 {
		return mcpServerPaths
	}

	var filteredFiles []string
	for _, file := range mcpServerPaths {
		serverName := strings.TrimSuffix(filepath.Base(file), filepath.Ext(file))

		if debugflags.Enabled("PROFILES") {
			ancli.Noticef("checking out: %v...", file)
		}
	INNER:
		for _, tool := range userConf.RequestedToolGlobs {
			toolSplit := strings.Split(tool, "_")
			// It can't have mcp prefix
			if len(toolSplit) < 2 {
				if debugflags.Enabled("PROFILES") {
					ancli.Noticef("SKIP: no mcp prefix, wrong length of split")
				}
				continue
			}
			// Its not a mcp server nor tool
			if toolSplit[0] != "mcp" {
				if debugflags.Enabled("PROFILES") {
					ancli.Noticef("SKIP: no mcp prefix")
				}
				continue
			}
			toolServer := toolSplit[1]
			hit := tools.WildcardMatch(toolServer, serverName)
			if hit {
				filteredFiles = append(filteredFiles, file)
				break INNER
			}
		}
	}

	return filteredFiles
}

// effectiveStartupMode resolves a server's configured startup field to a
// definite mode. An unrecognised value is rejected unconditionally and
// always: a value that reached here programmatically (not through
// pub_models.StartupMode.UnmarshalJSON's own parse-time rejection) is still
// an error, never a silent eager (R2-15). strictExplicit then overrides
// unconditionally: a server reached through agent.WithMcpServers while
// StrictMcpStartup is on always resolves eager, even when its own config
// says "lazy", because a warm cache would otherwise let setup skip
// connecting and no strict failure could exist to report (D35, R1-02). The
// unset default is lazy otherwise (D16, flipped by this phase now that the
// schema cache can supply a lazy server's tools without connecting).
//
// An unset mode for a command-based server whose command line clai cannot
// resolve is eager instead: a schema-cache hit would advertise a tool list
// carried over from a run that observed a different program, and no launcher
// evidence exists to prove otherwise. Connecting during setup observes the
// real tool list, so the capability is always told the truth about. An
// explicitly configured mode is never overridden: an operator who asked for
// one has accepted the trade, and a pinned or resolvable spec keeps lazy.
func effectiveStartupMode(server pub_models.McpServer, strictExplicit bool) (pub_models.StartupMode, error) {
	switch server.Startup {
	case "", pub_models.StartupEager, pub_models.StartupLazy:
	default:
		return "", claierr.NewMcpServerStartup(server.Name, "startup-mode",
			fmt.Errorf("unrecognised startup mode %q: want %q or %q", server.Startup, pub_models.StartupEager, pub_models.StartupLazy))
	}
	if strictExplicit {
		return pub_models.StartupEager, nil
	}
	if server.Startup != "" {
		return server.Startup, nil
	}
	if launcher.RequiresEagerConnect(server) {
		return pub_models.StartupEager, nil
	}
	return pub_models.StartupLazy, nil
}

// findConfiguredMcpServers delegates to serverconfig.FindConfiguredServers,
// the parsing primitive phase 7 relocated to its own leaf package so the
// tools listing's cache-only path (in internal/tools, which cannot import
// this package without a cycle) resolves a server's identity exactly the
// same way setup does.
func findConfiguredMcpServers(filePaths []string) ([]pub_models.McpServer, error) {
	return serverconfig.FindConfiguredServers(filePaths)
}

// debugRedactedMcpServer is the DEBUG dump's own shape for one server:
// args, env values and envfile contents are credential carriers (R1-03's
// documented `mcp-remote --header "Authorization: Bearer ..."` shape puts
// a token in args; env values are secrets by the same invariant 6
// amendment), so the dump prints only their shape — a count, or env's key
// names, never a value — instead of a verbatim json.Marshal of
// pub_models.McpServer, which carries them unredacted (R1-10).
type debugRedactedMcpServer struct {
	Name           string
	Command        string
	Url            string
	ArgCount       int
	EnvKeys        []string
	EnvFileSet     bool
	Startup        pub_models.StartupMode
	AuthConfigured bool
}

// redactMcpServersForDebug builds the DEBUG dump's redacted view of
// servers, env key names sorted so the dump is deterministic.
func redactMcpServersForDebug(servers []pub_models.McpServer) []debugRedactedMcpServer {
	out := make([]debugRedactedMcpServer, len(servers))
	for i, s := range servers {
		keys := make([]string, 0, len(s.Env))
		for k := range s.Env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		out[i] = debugRedactedMcpServer{
			Name:           s.Name,
			Command:        s.Command,
			Url:            s.Url,
			ArgCount:       len(s.Args),
			EnvKeys:        keys,
			EnvFileSet:     s.EnvFile != "",
			Startup:        s.Startup,
			AuthConfigured: s.Auth != nil,
		}
	}
	return out
}

// setupMcpManager loads MCP server configurations from a directory.
// Each file inside the directory should contain a single MCP server configuration.
// Every server is started and its tools registered with a prefix of the filename.
// If the directory is missing, an error is returned. With
// userConf.SkipAmbientMcpServers the directory is never read and only
// userConf.McpServers reach the manager (D18).
// sink receives every server's stderr lines; on setup failure the buffered
// error lines are flushed to stderr so the failure reason stays visible.
// cache backs a lazy-resolved command-based server's tool schemas; nil
// disables caching for the run (every lookup is a miss) rather than failing
// setup.
// authz resolves an endpoint-based server's credential (phase 5); nil keeps
// every HTTP server unauthenticated exactly as before this phase, so an
// endpoint demanding authorization fails with claierr.AuthChallengeError
// rather than being silently skipped.
//
// A server discovered from the config directory is ambient: a startup failure
// keeps today's warn-and-degrade. A server in userConf.McpServers is explicit
// when the run is agent-driven (AgentSettings.StrictMcpStartup); its failures
// are collected while the in-setup wait runs and joined into a typed error on
// return, so a caller whose task depends on the server can see that it is
// absent (worklog 2026-09-05-error-propagation, D13).
func setupMcpManager(ctx context.Context, mcpServersDir string, userConf Configurations, sink mcp.ServerLogSink, cache *schemacache.Cache, authz *mcpauth.Authorizer) (map[string]pub_models.LLMTool, error) {
	var files []string
	if !userConf.SkipAmbientMcpServers {
		if _, err := os.Stat(mcpServersDir); os.IsNotExist(err) {
			return nil, fmt.Errorf("MCP servers directory not found at %s. If you want MCP server support, create one using 'clai setup' and select option 3", mcpServersDir)
		}
		var err error
		files, err = filepath.Glob(filepath.Join(mcpServersDir, "*.json"))
		if err != nil {
			return nil, fmt.Errorf("failed to list mcp server configs: %w", err)
		}
	}

	// Filter MCP servers based on profile tools
	filteredFiles := filterMcpServersByProfile(files, userConf)
	mcpServers, err := findConfiguredMcpServers(filteredFiles)
	explicit := userConf.AgentSettings != nil && userConf.AgentSettings.StrictMcpStartup
	// userConf.McpServers never passed through FindConfiguredServers, so its
	// own transport XOR was never enforced: both command and url set
	// silently preferred url, and neither set reached
	// exec.CommandContext(ctx, "") (D43, R2-07). validExplicit is validated
	// exactly as a config-directory file is.
	validExplicit, transportFailures := validateExplicitServers(explicit, userConf.McpServers)
	// The config-dir servers are ambient; the userConf.McpServers tail is
	// explicit exactly when the run is agent-driven. Profile-sourced servers
	// ride the CLI path and stay ambient (D13: only WithMcpServers is
	// load-bearing).
	explicitTail := len(mcpServers)
	mcpServers = append(mcpServers, validExplicit...)

	if misc.Truthy(os.Getenv("DEBUG")) {
		ancli.Okf("mcpServers: %v", debug.IndentedJsonFmt(redactMcpServersForDebug(mcpServers)))
	}
	// A per-file parse or validation failure must never be silently dropped
	// just because some other file in the same directory parsed (D44,
	// R2-06): before this, the joined error from findConfiguredMcpServers
	// was read only inside the len(mcpServers)==0 branch below, so a single
	// bad file among several good ones vanished with no message at all. It
	// is reported unconditionally here and, under strict startup, also
	// joined into the explicit failures returned to the caller.
	if err != nil {
		ancli.Warnf("failed to parse mcp server config(s): %v\n", err)
	}
	if len(mcpServers) == 0 {
		if err != nil {
			return nil, fmt.Errorf("failed to find mcpServers: %w", err)
		}
		if len(transportFailures) > 0 {
			return map[string]pub_models.LLMTool{}, errors.Join(transportFailures...)
		}
		// Nothing to do, no need to start mcp.Manager etc, just return
		return map[string]pub_models.LLMTool{}, nil
	}

	// Per-run registry: MCP tools discovered below are scoped to this run's
	// querier. They must never be written into the process-global tools
	// registry, whose entries are overwritten by every concurrent Setup.
	runReg := tools.NewRegistry()

	controlChannel := make(chan mcp.ControlEvent)
	// Buffered per server: a failure report is sent before its server's
	// WaitGroup Done, so once the wait below returns every report is already
	// in the channel.
	startupFailures := make(chan mcp.StartupFailure, len(mcpServers))

	toolWg := sync.WaitGroup{}
	toolWg.Add(len(mcpServers))
	go mcp.Manager(ctx, controlChannel, &toolWg, runReg, startupFailures)

	failures := &failureCollector{errs: append([]error(nil), transportFailures...)}
	if err != nil && explicit {
		failures.addExplicit("config-dir", claierr.NewMcpServerStartup("config-dir", "parse", err))
	}
	for i, mcpServer := range mcpServers {
		isExplicitIdx := i >= explicitTail
		strictExplicit := explicit && isExplicitIdx
		isHTTP := mcpServer.Url != ""

		mode, modeErr := effectiveStartupMode(mcpServer, strictExplicit)
		if modeErr != nil {
			failures.addIfExplicit(explicit, isExplicitIdx, mcpServer.Name, modeErr)
			toolWg.Done()
			continue
		}

		if mode == pub_models.StartupLazy {
			// Schema-cache-aware path: a hit registers tools with no
			// transport; a miss connects now and captures the entry (D18).
			// An endpoint-based server additionally wires cache
			// invalidation onto its connector (phase 4). Run as its own
			// goroutine, joined through toolWg exactly as mcp.Manager
			// already does for an eager server: a miss still connects, and
			// D18's "exactly as today" means concurrently, not one server
			// at a time (D39, R2-02).
			go func(mcpServer pub_models.McpServer, isExplicitIdx, isHTTP bool) {
				defer toolWg.Done()
				var err error
				if isHTTP {
					err = resolveLazyHttpServerViaCache(ctx, cache, mcpServer, sink, runReg, authz, userConf.OutputIsTerminalOrDefault())
				} else {
					err = resolveLazyServerViaCache(ctx, cache, mcpServer, sink, runReg, userConf.OutputIsTerminalOrDefault())
				}
				if err != nil {
					failures.addIfExplicit(explicit, isExplicitIdx, mcpServer.Name, err)
				}
			}(mcpServer, isExplicitIdx, isHTTP)
			continue
		}
		// No context leak here as it's a child of the root context, which will cascade the cancel
		// for all other code paths
		clientContext, clientContextCancel := context.WithCancel(ctx)
		var conn mcp.Conn
		var connErr error
		if isHTTP {
			// Eager HTTP servers get only the static half of the
			// credential-precedence chain (auth.token_command,
			// auth.token_env): the interactive flow needs a challenge from
			// a real connect attempt, which this path hands straight to
			// Manager instead of driving itself (phase 5 scope; the lazy
			// path, the default for an ambient endpoint-based server since
			// D16/D18, gets the full chain).
			var decorator mcp.RequestDecorator
			decorator, connErr = staticHttpDecorator(ctx, mcpServer)
			var opts []mcp.HttpConnOption
			if connErr == nil && decorator != nil {
				opts = append(opts, mcp.WithRequestDecorator(decorator))
			}
			if connErr == nil {
				conn = mcp.NewHttpConn(clientContext, mcpServer, sink, opts...)
			}
		} else {
			conn, connErr = mcp.NewStdioConn(clientContext, mcpServer, sink)
		}
		if connErr != nil {
			// NewStdioConn already returns a typed *claierr.McpServerStartupError
			// naming the "spawn" stage, so it is propagated as-is rather than
			// re-wrapped.
			failures.addIfExplicit(explicit, isExplicitIdx, mcpServer.Name, connErr)
			toolWg.Done()
			clientContextCancel()
			continue
		}

		controlChannel <- mcp.ControlEvent{
			ServerName:  mcpServer.Name,
			Server:      mcpServer,
			Conn:        conn,
			Cancel:      clientContextCancel,
			OnHandshake: mcpSchemaCapture(cache, mcpServer),
		}
	}

	done := make(chan struct{})
	go func() {
		toolWg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	close(startupFailures)
	for f := range startupFailures {
		if explicit && f.ServerName != "" && isExplicitServer(f.ServerName, userConf.McpServers) {
			failures.addExplicit(f.ServerName, f.Err)
			continue
		}
		ancli.Warnf("failed to setup mcp server '%v': %v\n", f.ServerName, f.Err)
	}
	if explicitFailures := failures.all(); len(explicitFailures) > 0 {
		return runReg.All(), errors.Join(explicitFailures...)
	}
	notifyMcpSetupSucceeded(sink)
	return runReg.All(), nil
}

// isExplicitServer reports whether name is one of the explicitly requested
// servers. Handshake-failure reports carry only the server name, so the
// lookup is by name; two servers sharing a name already collide in the
// mcp_<name>_<tool> registration prefix, so a duplicate is not a new case.
func isExplicitServer(name string, servers []pub_models.McpServer) bool {
	for _, s := range servers {
		if s.Name == name {
			return true
		}
	}
	return false
}

// failureCollector routes and accumulates server startup failures: an
// explicit server under strict startup is joined for the caller to return;
// every other server keeps the legacy warn-and-degrade. The lazy branch
// resolves concurrently (D39, R2-02), so every write is serialised through
// mu rather than through the sequential append classifyServerFailure used
// to make safe for free.
type failureCollector struct {
	mu   sync.Mutex
	errs []error
}

func (f *failureCollector) addIfExplicit(explicit, isExplicitIdx bool, serverName string, err error) {
	if explicit && isExplicitIdx {
		f.addExplicit(serverName, err)
		return
	}
	ancli.Warnf("failed to setup: '%v', err: %v\n", serverName, err)
}

// addExplicit is the single funnel every explicit server's failure passes
// through before setupTooling's strict/degrade fork reads it. That fork
// classifies on claierr.ErrMcpServerStartup alone (errors.Is), so a
// producer that returns a different typed error class — a credential
// source failure, a discovery failure, an unresolved auth challenge — must
// still unwrap to the sentinel here or the explicit failure silently
// degrades to a warning and Setup returns nil (D42, R2-05). An error that
// already carries the sentinel is kept as-is so its own stage name
// survives; only an unwrapped error is given the generic "credential"
// stage, since every producer reaching this funnel unwrapped today is a
// credential-resolution failure on the HTTP auth path.
func (f *failureCollector) addExplicit(serverName string, err error) {
	if !errors.Is(err, claierr.ErrMcpServerStartup) {
		err = claierr.NewMcpServerStartup(serverName, "credential", err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.errs = append(f.errs, err)
}

func (f *failureCollector) all() []error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.errs
}

// validateExplicitServers runs serverconfig.ValidateTransport over every
// server passed through agent.WithMcpServers, exactly as
// findConfiguredMcpServers already does for a config-directory file (D43,
// R2-07): without it, a struct with both command and url set silently
// preferred url, and one with neither set reached
// exec.CommandContext(ctx, ""). A server that fails validation is routed by
// the same explicit/ambient posture a startup failure would be and never
// joins valid, so it never reaches the manager.
func validateExplicitServers(explicit bool, servers []pub_models.McpServer) (valid []pub_models.McpServer, failures []error) {
	valid = make([]pub_models.McpServer, 0, len(servers))
	for _, server := range servers {
		if err := serverconfig.ValidateTransport(server.Name, server); err != nil {
			typed := claierr.NewMcpServerStartup(server.Name, "config", err)
			if explicit {
				failures = append(failures, typed)
			} else {
				ancli.Warnf("failed to setup: '%v', err: %v\n", server.Name, typed)
			}
			continue
		}
		valid = append(valid, server)
	}
	return valid, failures
}

// resolveLazyServerViaCache is the schema-cache-aware path for a
// lazy-resolved command-based server. A hit registers its tools wired to an
// unresolved Connector, so no transport is constructed. A miss connects now
// — the zero-process outcome is a warm-cache outcome, never a cold one
// (D18) — registers from the live handshake exactly as a hit would, and
// captures the entry. A capture failure degrades to a warning: the cache is
// an optimisation, never a dependency. outputIsTerminal resolves the
// auth-timeout parameter (phase 6, D22): a stdio tool carries no
// AuthResolver, since the only lever on a mid-connect prompt is time.
func resolveLazyServerViaCache(ctx context.Context, cache *schemacache.Cache, server pub_models.McpServer, sink mcp.ServerLogSink, registrar mcp.ToolRegistrar, outputIsTerminal bool) error {
	identity := schemacache.BuildIdentity(server)
	timeout := time.Duration(server.TimeoutSeconds) * time.Second
	authTimeout := resolveAuthTimeout(server, outputIsTerminal)

	if cache != nil {
		if rec, ok := cache.Lookup(identity); ok {
			connector := mcp.NewConnector(ctx, server, sink, connectorOptsFor(server)...)
			invalidating := newCacheInvalidatingConnector(ctx, connector, cache, identity, server.Name)
			return mcp.RegisterTools(rec.Tools, server.Name, invalidating, timeout, registrar, nil, authTimeout)
		}
	}

	connCtx, cancel := context.WithCancel(ctx)
	// Only cancel on failure; on success the connection must stay alive to
	// serve tool calls for the rest of the run, cleaned up when ctx ends.
	var connected bool
	defer func() {
		if !connected {
			cancel()
		}
	}()

	conn, err := mcp.NewStdioConn(connCtx, server, sink)
	if err != nil {
		return err
	}

	// The connect-bound, not the connection-level handshake bound, wraps
	// spawn plus handshake here: this is the cache-miss connect, the same
	// one NewConnector bounds internally, and connect_timeout_seconds must
	// have an effect on it or a hung endpoint blocks cold setup for the
	// 30s handshake default regardless of what the server configures
	// (R1-14).
	connectCtx, connectCancel := context.WithTimeout(ctx, mcp.ConnectBoundOf(server))
	serverInfo, toolsArray, err := mcp.Handshake(connectCtx, conn, server.Name)
	connectCancel()
	if err != nil {
		return mcp.ReportAsConnectStage(err, connectCtx, server.Name)
	}

	registerConn := conn
	if cache != nil {
		registerConn = newCacheInvalidatingConn(conn, cache, identity, server.Name)
	}
	if err := mcp.RegisterTools(toolsArray, server.Name, mcp.NewResolvedConnector(registerConn), timeout, registrar, nil, authTimeout); err != nil {
		return err
	}
	connected = true

	if cache != nil {
		if captureErr := cache.Capture(identity, mcp.ProtocolVersion, serverInfo, toolsArray); captureErr != nil {
			ancli.Warnf("failed to persist mcp schema cache entry for %q: %v\n", server.Name, captureErr)
		}
		if watcher, ok := conn.(mcp.NotificationWatcher); ok {
			go watchForToolsListChanged(connCtx, watcher, cache, identity, server.Name)
		}
	}
	return nil
}

// mcpSchemaCapture returns the callback the eager Manager path runs with a
// successful handshake's observations, so an eagerly connected server's tool
// list reaches the schema cache too. The lazy path captures on a miss already;
// without this an eager server would never appear in the cache-only tools
// listing, which never connects. A nil cache disables capture, as everywhere
// else, and a capture failure degrades to a warning because the cache is an
// optimisation, never a dependency.
func mcpSchemaCapture(cache *schemacache.Cache, server pub_models.McpServer) func(serverInfo, toolsArray json.RawMessage) {
	if cache == nil {
		return nil
	}
	identity := schemacache.BuildIdentity(server)
	return func(serverInfo, toolsArray json.RawMessage) {
		if err := cache.Capture(identity, mcp.ProtocolVersion, serverInfo, toolsArray); err != nil {
			ancli.Warnf("failed to persist mcp schema cache entry for %q: %v\n", server.Name, err)
		}
	}
}

// connectorOptsFor carries a configured connect_timeout_seconds onto the
// Connector a cache hit constructs. Zero is left alone so the connector
// keeps its own default rather than a zero-duration bound.
func connectorOptsFor(server pub_models.McpServer) []mcp.ConnectorOption {
	if server.ConnectTimeoutSeconds > 0 {
		return []mcp.ConnectorOption{mcp.WithConnectBound(time.Duration(server.ConnectTimeoutSeconds) * time.Second)}
	}
	return nil
}

// newMcpSchemaCache builds the production schema cache under the clai cache
// dir. A failure to resolve that directory disables caching for the run
// (every lookup degrades to a miss) rather than failing setup: the cache is
// an optimisation, never a dependency.
func newMcpSchemaCache() (*schemacache.Cache, error) {
	cacheDir, err := utils.GetClaiCacheDir()
	if err != nil {
		return nil, fmt.Errorf("resolve clai cache dir: %w", err)
	}
	return schemacache.New(path.Join(cacheDir, schemacache.DefaultDirName))
}

// notifyMcpSetupSucceeded tells a draining sink that MCP setup completed, so
// the pre-session auth window can be cleared in place.
func notifyMcpSetupSucceeded(sink mcp.ServerLogSink) {
	if s, ok := sink.(interface{ setupSucceeded() }); ok {
		s.setupSucceeded()
	}
}

func setupTooling[C models.StreamCompleter](ctx context.Context, modelConf C, userConf *Configurations, sink mcp.ServerLogSink) error {
	toolBox, ok := any(modelConf).(models.ToolBox)
	if !ok {
		return nil
	}
	if userConf.UseSkills {
		registerTool(toolBox, userConf, pkgtools.LoadSkill)
	}
	// Lookback tools are internal markers dispatched by the tool executor; like
	// load_skill they are registered whenever the feature is active, independent of
	// -t/-tools narrowing and even when external tools are disabled.
	if userConf.UseLookback {
		registerTool(toolBox, userConf, pkgtools.SearchConversations)
		registerTool(toolBox, userConf, pkgtools.InspectConversation)
		registerTool(toolBox, userConf, pkgtools.ReadMessage)
	}
	if !userConf.UseTools {
		return nil
	}
	tools.Init()
	cache, cacheErr := newMcpSchemaCache()
	if cacheErr != nil {
		ancli.Warnf("mcp schema cache unavailable, every lazy server will connect at setup: %v\n", cacheErr)
	}
	authz := newMcpAuthorizer(userConf.ConfigDir, userConf.TrustInput, userConf.OutputIsTerminalOrDefault(), sink)
	// A credential command is subject to the same command-ban policy a
	// tool-call context carries, so a banned command cannot be smuggled in
	// as a credential helper (R1-05): every credential resolution this
	// setup drives runs under this same ctx, so attaching the policy here
	// reaches every call site in one place rather than at each producer.
	mcpCtx := pkgtools.WithCmdBanContext(ctx, userConf.CmdBan)
	mcpTools, err := setupMcpManager(mcpCtx, path.Join(userConf.ConfigDir, "mcpServers"), *userConf, sink, cache, authz)
	if misc.Truthy(os.Getenv("DEBUG")) {
		ancli.Okf("Registering tools on querier of type: %T\n", modelConf)
	}
	if err != nil {
		if errors.Is(err, claierr.ErrMcpServerStartup) {
			// D13: an explicitly requested server failed to start, so the setup
			// fails rather than silently running without the requested tools.
			return fmt.Errorf("failed to start explicitly requested MCP servers: %w", err)
		}
		// Ambient and environment failures keep the legacy degrade: a missing
		// or broken config-dir server is not by itself terminal.
		ancli.Warnf("failed to add mcp tools: %v", err)
		return nil
	}

	// available is this run's selectable tool catalog: the static local tools
	// plus the MCP tools this run started. Dynamic tools (MCP and WithTools)
	// are never written into the process-global registry, so concurrent Setups
	// cannot overwrite each other's instances.
	available := tools.Registry.All()
	maps.Copy(available, mcpTools)

	// If usetools and no specific tools chosen, assume all are valid
	if len(userConf.RequestedToolGlobs) == 0 && len(userConf.Tools) == 0 {
		allTools := make([]pub_models.LLMTool, 0, len(available))
		for _, tool := range available {
			allTools = append(allTools, tool)
		}
		for _, tool := range uniqueTools(allTools) {
			registerTool(toolBox, userConf, tool)
		}
		if userConf.BaseTools == nil {
			userConf.BaseTools = available
		}
		return nil
	}
	toAdd := make([]pub_models.LLMTool, 0)
	for _, t := range userConf.RequestedToolGlobs {
		if strings.Contains(t, "*") {
			matchingTools := matchingTools(available, t)
			if len(matchingTools) == 0 {
				ancli.Warnf("attempted to find tools using wildcard search: '%v', found none\n", t)
			}
			toAdd = append(toAdd, matchingTools...)
			continue
		}
		tool, exists := available[t]
		if !exists {
			ancli.Warnf("attempted to find tool: '%v', which doesn't exist, skipping\n", t)
			continue
		}
		toAdd = append(toAdd, tool)
	}

	toAdd = uniqueTools(toAdd)
	for _, tool := range toAdd {
		if misc.Truthy(os.Getenv("DEBUG")) {
			ancli.PrintOK(fmt.Sprintf("\tname: %v, desc: %v\n", tool.Specification().Name, tool.Specification().Description))
		}
		registerTool(toolBox, userConf, tool)
		if userConf.BaseTools == nil {
			userConf.BaseTools = map[string]pub_models.LLMTool{}
		}
		userConf.BaseTools[tool.Specification().Name] = tool
	}

	registeredNames := make(map[string]struct{}, len(toAdd))
	for _, tool := range toAdd {
		registeredNames[tool.Specification().Name] = struct{}{}
	}
	for _, t := range uniqueTools(userConf.Tools) {
		if _, exists := registeredNames[t.Specification().Name]; exists {
			continue
		}
		registerTool(toolBox, userConf, t)
		if userConf.BaseTools == nil {
			userConf.BaseTools = map[string]pub_models.LLMTool{}
		}
		userConf.BaseTools[t.Specification().Name] = t
		registeredNames[t.Specification().Name] = struct{}{}
	}
	return nil
}

// matchingTools returns every tool in available whose name matches pattern.
// It mirrors tools.Registry.WildcardGet over a caller-owned map so tool
// selection reads the per-run catalog instead of the process-global registry.
func matchingTools(available map[string]pub_models.LLMTool, pattern string) []pub_models.LLMTool {
	var matches []pub_models.LLMTool
	for name, tool := range available {
		if tools.WildcardMatch(pattern, name) {
			matches = append(matches, tool)
		}
	}
	return matches
}

func registerTool(toolBox models.ToolBox, userConf *Configurations, tool pub_models.LLMTool) {
	if userConf.RegisteredTools == nil {
		userConf.RegisteredTools = map[string]struct{}{}
	}
	name := tool.Specification().Name
	if _, exists := userConf.RegisteredTools[name]; exists {
		return
	}
	toolBox.RegisterTool(tool)
	userConf.RegisteredTools[name] = struct{}{}
}

// uniqueTools removes aliases and repeated selections before tool schemas are
// sent to a model. Providers reject a request when two schemas have the same
// specification name, even when the registry entries came from distinct
// aliases.
func uniqueTools(input []pub_models.LLMTool) []pub_models.LLMTool {
	seen := make(map[string]struct{}, len(input))
	ret := make([]pub_models.LLMTool, 0, len(input))
	for _, tool := range input {
		name := tool.Specification().Name
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		ret = append(ret, tool)
	}
	return ret
}
