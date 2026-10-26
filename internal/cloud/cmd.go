package cloud

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/baalimago/clai/internal"
	"github.com/baalimago/go_away_boilerplate/pkg/cmd"
)

// CommandDeps carries what the cloud command needs from the composition
// root: the dirs, the conversation hooks, and seams tests replace.
type CommandDeps struct {
	ConfDir  func() (string, error)
	CacheDir func() (string, error)
	Hooks    Hooks
	Out      io.Writer
	Now      func() time.Time
	// Sleep waits between device-login polls.
	Sleep func(ctx context.Context, d time.Duration) error
	// Hostname names the device on the account page.
	Hostname func() (string, error)
}

func (d CommandDeps) withDefaults() CommandDeps {
	if d.Out == nil {
		d.Out = os.Stdout
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Sleep == nil {
		d.Sleep = func(ctx context.Context, dur time.Duration) error {
			t := time.NewTimer(dur)
			defer t.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-t.C:
				return nil
			}
		}
	}
	if d.Hostname == nil {
		d.Hostname = os.Hostname
	}
	return d
}

// UsageError is a cloud subcommand given the wrong arguments.
type UsageError struct{ Msg string }

func (e *UsageError) Error() string { return e.Msg }

// LoginError is a device login the browser refused or let expire.
type LoginError struct{ Code string }

func (e *LoginError) Error() string {
	if e.Code == "access_denied" {
		return "the login was refused in the browser"
	}
	return "the login code expired before it was approved: run 'clai cloud login' again"
}

const helpText = `cloud <subcommand>. Sync this machine's configs and conversations with clai.chat.

The config store (profiles, MCP servers, shell contexts, skills, mode
configs, theme) is free. The conversation store (conversations, their
images and the conversations clai imports from other tools) needs a
$1/month subscription on your clai.chat account page.

Once logged in, clai syncs in the background after a command, at most once
a minute, and never makes a command wait. API keys are never uploaded: a
config file that holds one is skipped. Set CLAI_CLOUD=off to stop the
background sync; CLAI_CLOUD_TOKEN and CLAI_CLOUD_URL stand in for a login.

Subcommands:
  login [-url URL] [-name NAME]   Approve this machine in the browser
  logout                          Revoke this machine's login
  status                          Show the account, the stores and the last sync
  sync [config|conversations]     Sync now
  enable <store>                  Sync a store on this machine
  disable <store>                 Stop syncing a store on this machine

Examples:
  clai cloud login
  clai cloud enable conversations
  clai cloud sync`

// Command builds the cloud command tree.
func Command(deps CommandDeps) *internal.Command {
	deps = deps.withDefaults()
	c := &internal.Command{Name: "cloud", Desc: "Sync configs and conversations with clai.chat", HelpText: helpText}
	c.OnRun = func(_ context.Context, c *internal.Command) error {
		_, err := fmt.Fprintln(deps.Out, c.Help())
		return err
	}

	var loginURL, loginName string
	login := &internal.Command{
		Name:     "login",
		Desc:     "Log this machine in to clai.chat",
		HelpText: "cloud login. Prints a code to approve on clai.chat, waits for it, then syncs the config store.",
		Register: func(fs *flag.FlagSet) {
			fs.StringVar(&loginURL, "url", "", "the service to log in to (default "+DefaultURL+" or CLAI_CLOUD_URL)")
			fs.StringVar(&loginName, "name", "", "this device's name on the account page (default: the hostname)")
		},
	}
	login.OnRun = func(ctx context.Context, _ *internal.Command) error {
		return runLogin(ctx, deps, loginURL, loginName)
	}

	logout := &internal.Command{Name: "logout", Desc: "Revoke this machine's clai.chat login", HelpText: "cloud logout. Revokes the token on clai.chat and forgets this machine's sync state. Local files stay."}
	logout.OnRun = func(ctx context.Context, _ *internal.Command) error { return runLogout(ctx, deps) }

	status := &internal.Command{Name: "status", Desc: "Show the account, the stores and the last sync", HelpText: "cloud status."}
	status.OnRun = func(ctx context.Context, _ *internal.Command) error { return runStatus(ctx, deps) }

	var background bool
	syncCmd := &internal.Command{
		Name:     "sync",
		Desc:     "Sync now",
		HelpText: "cloud sync [config|conversations]. Syncs the named store, or every enabled one.",
		Register: func(fs *flag.FlagSet) {
			fs.BoolVar(&background, "background", false, "run as the background sync: skip when another sync holds the lock, and go round again when one asked to")
		},
	}
	syncCmd.OnRun = func(ctx context.Context, c *internal.Command) error {
		return runSync(ctx, deps, c.Args()[1:], background)
	}

	toggle := func(name string, on bool) *internal.Command {
		t := &internal.Command{Name: name, Desc: name + " a store on this machine", HelpText: "cloud " + name + " <config|conversations>."}
		t.OnRun = func(ctx context.Context, c *internal.Command) error { return runToggle(ctx, deps, c.Args()[1:], on) }
		return t
	}
	c.Subs = map[string]cmd.Command{
		"login":   login,
		"logout":  logout,
		"status":  status,
		"sync":    syncCmd,
		"enable":  toggle("enable", true),
		"disable": toggle("disable", false),
	}
	return c
}

func runLogin(ctx context.Context, deps CommandDeps, url, name string) error {
	confDir, err := deps.ConfDir()
	if err != nil {
		return err
	}
	creds, _ := LoadCredentials(confDir)
	if url != "" {
		creds.URL = url
	}
	if name == "" {
		name, _ = deps.Hostname()
	}
	client := NewClient(creds.URL, "")
	code, err := client.StartDevice(ctx, name)
	if err != nil {
		return err
	}
	fmt.Fprintf(deps.Out, "Open %s\nand approve the code %s for %q.\nWaiting for approval...\n", code.VerificationURIComplete, code.UserCode, name)
	interval := time.Duration(max(code.Interval, 1)) * time.Second
	deadline := deps.Now().Add(time.Duration(code.ExpiresIn) * time.Second)
	for {
		if err := deps.Sleep(ctx, interval); err != nil {
			return err
		}
		tok, err := client.PollDevice(ctx, code.DeviceCode)
		if err == nil {
			creds.Token, creds.Device = tok.AccessToken, tok.DeviceName
			break
		}
		apiErr, ok := IsAPIError(err)
		if !ok {
			return err
		}
		switch apiErr.Code {
		case "authorization_pending":
		case "slow_down":
			interval += 5 * time.Second
		case "access_denied", "expired_token":
			return &LoginError{Code: apiErr.Code}
		default:
			return err
		}
		if deps.Now().After(deadline) {
			return &LoginError{Code: "expired_token"}
		}
	}
	client.Token = creds.Token
	if me, err := client.Me(ctx); err == nil {
		creds.Account = me.Account
	}
	// A new login starts from the server's state: this machine's old sync
	// state may belong to another account.
	if err := resetState(confDir); err != nil {
		return err
	}
	if err := saveCredentials(confDir, creds); err != nil {
		return err
	}
	fmt.Fprintf(deps.Out, "Logged in as %s on %s.\n", orDash(creds.Account), creds.URL)
	return runSync(ctx, deps, nil, false)
}

func runLogout(ctx context.Context, deps CommandDeps) error {
	confDir, err := deps.ConfDir()
	if err != nil {
		return err
	}
	creds, err := LoadCredentials(confDir)
	var notIn *NotLoggedInError
	if errors.As(err, &notIn) {
		_, err := fmt.Fprintln(deps.Out, "Not logged in.")
		return err
	}
	if err != nil {
		return err
	}
	if err := NewClient(creds.URL, creds.Token).RevokeCurrent(ctx); err != nil {
		if apiErr, ok := IsAPIError(err); !ok || !apiErr.Unauthorized() {
			return fmt.Errorf("revoke the login on %s: %w", creds.URL, err)
		}
	}
	if err := errors.Join(removeCredentials(confDir), resetState(confDir)); err != nil {
		return err
	}
	_, err = fmt.Fprintln(deps.Out, "Logged out. Your local files are unchanged.")
	return err
}

func runStatus(ctx context.Context, deps CommandDeps) error {
	confDir, err := deps.ConfDir()
	if err != nil {
		return err
	}
	creds, err := LoadCredentials(confDir)
	if err != nil {
		return err
	}
	settings, err := LoadSettings(confDir)
	if err != nil {
		return err
	}
	me, err := NewClient(creds.URL, creds.Token).Me(ctx)
	if err != nil {
		return err
	}
	w := deps.Out
	fmt.Fprintf(w, "Account:  %s on %s\nDevice:   %s\n", me.Account, creds.URL, me.Device)
	for _, s := range Stores {
		info := me.Stores[s]
		on := "off"
		if settings.Enabled(s) {
			on = "on"
		}
		fmt.Fprintf(w, "%-14s sync %s, %d files, %s of %s\n", string(s)+":", on, info.Files, humanBytes(info.Bytes), humanBytes(Quotas[s].Bytes))
	}
	fmt.Fprintf(w, "Subscription: %s\n", subscriptionLine(me, deps.Now()))
	if cacheDir, err := deps.CacheDir(); err == nil {
		last := "never"
		if t := LastSync(cacheDir); !t.IsZero() {
			last = t.Local().Format(time.DateTime)
		}
		fmt.Fprintf(w, "Last sync: %s (log: %s)\n", last, LogPath(cacheDir))
	}
	return nil
}

func subscriptionLine(me Me, now time.Time) string {
	day := func(ms int64) string { return time.UnixMilli(ms).Local().Format(time.DateOnly) }
	switch me.Conversations.Kind {
	case "active":
		if me.Conversations.RenewsAt > 0 {
			return "active, renews " + day(me.Conversations.RenewsAt)
		}
		return "active"
	case "grace":
		return "payment failed; still writable until " + day(me.Conversations.Until)
	case "lapsed":
		return "ended; read-only until " + day(me.Conversations.DeleteAt) + ", then deleted"
	}
	if !me.Billing {
		return "none (subscriptions are not open yet)"
	}
	return "none: subscribe on your clai.chat account page to sync conversations"
}

func runSync(ctx context.Context, deps CommandDeps, args []string, background bool) error {
	confDir, err := deps.ConfDir()
	if err != nil {
		return err
	}
	cacheDir, err := deps.CacheDir()
	if err != nil {
		return err
	}
	creds, err := LoadCredentials(confDir)
	if err != nil {
		if background {
			return nil
		}
		return err
	}
	settings, err := LoadSettings(confDir)
	if err != nil {
		return err
	}
	stores := []Store{}
	for _, a := range args {
		s, ok := ParseStore(a)
		if !ok {
			return &UsageError{Msg: fmt.Sprintf("unknown store %q: use config or conversations", a)}
		}
		stores = append(stores, s)
	}
	if len(stores) == 0 {
		for _, s := range Stores {
			if settings.Enabled(s) {
				stores = append(stores, s)
			}
		}
	}
	touch(lastPath(cacheDir), deps.Now())
	release, err := acquire(cacheDir, deps.Now())
	if err != nil {
		var locked *LockedError
		if background && errors.As(err, &locked) {
			return nil
		}
		return err
	}
	defer release()
	engine := &Engine{Client: NewClient(creds.URL, creds.Token), ConfDir: confDir, Hooks: deps.Hooks, Now: deps.Now}
	if background {
		fmt.Fprintf(deps.Out, "%s sync\n", deps.Now().UTC().Format(time.RFC3339))
	}
	var errs []error
	// A background sync goes round again when a command asked for one
	// while it ran, at most a few times.
	for range 3 {
		errs = errs[:0]
		for _, s := range stores {
			rep, err := engine.Sync(ctx, s)
			printReport(deps.Out, rep, err)
			if err != nil {
				errs = append(errs, err)
			}
		}
		if !background || !takeDirty(cacheDir) {
			break
		}
	}
	return errors.Join(errs...)
}

func printReport(w io.Writer, rep Report, err error) {
	if err != nil {
		if apiErr, ok := IsAPIError(err); ok && apiErr.SubscriptionRequired() {
			fmt.Fprintf(w, "%s: %s\n", rep.Store, apiErr.Message)
			return
		}
	}
	parts := []string{}
	if rep.Downloaded > 0 {
		parts = append(parts, fmt.Sprintf("%d pulled", rep.Downloaded))
	}
	if rep.Uploaded > 0 {
		parts = append(parts, fmt.Sprintf("%d uploaded", rep.Uploaded))
	}
	if rep.Removed > 0 {
		parts = append(parts, fmt.Sprintf("%d removed", rep.Removed))
	}
	if len(parts) == 0 {
		parts = append(parts, "up to date")
	}
	if err == nil {
		fmt.Fprintf(w, "%s: %s\n", rep.Store, strings.Join(parts, ", "))
	}
	for _, c := range rep.Conflicts {
		fmt.Fprintf(w, "  conflict: %s\n", c)
	}
	for _, s := range rep.Skipped {
		fmt.Fprintf(w, "  skipped %s: %s\n", s.Path, s.Reason)
	}
}

func runToggle(ctx context.Context, deps CommandDeps, args []string, on bool) error {
	if len(args) != 1 {
		return &UsageError{Msg: "name one store: config or conversations"}
	}
	store, ok := ParseStore(args[0])
	if !ok {
		return &UsageError{Msg: fmt.Sprintf("unknown store %q: use config or conversations", args[0])}
	}
	confDir, err := deps.ConfDir()
	if err != nil {
		return err
	}
	settings, err := LoadSettings(confDir)
	if err != nil {
		return err
	}
	settings.Stores[store] = on
	if err := saveSettings(confDir, settings); err != nil {
		return err
	}
	if !on {
		_, err := fmt.Fprintf(deps.Out, "%s no longer syncs on this machine.\n", store)
		return err
	}
	fmt.Fprintf(deps.Out, "%s syncs on this machine.\n", store)
	creds, err := LoadCredentials(confDir)
	if err != nil {
		_, err := fmt.Fprintln(deps.Out, "Run 'clai cloud login' to start.")
		return err
	}
	if store == StoreConversations {
		if me, err := NewClient(creds.URL, creds.Token).Me(ctx); err == nil && !me.Stores[store].Writable {
			fmt.Fprintf(deps.Out, "The conversation store needs a subscription: %s/account/#conversations\n", strings.TrimRight(creds.URL, "/"))
		}
	}
	return nil
}

func humanBytes(n int64) string {
	switch {
	case n >= 1_000_000_000:
		return fmt.Sprintf("%.1f GB", float64(n)/1e9)
	case n >= 1_000_000:
		return fmt.Sprintf("%.1f MB", float64(n)/1e6)
	}
	return fmt.Sprintf("%.1f kB", float64(n)/1e3)
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
