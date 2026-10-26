package cloudcmd

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/baalimago/clai/internal/cloud"
	"github.com/baalimago/clai/internal/cloud/sync"
)

type runner struct{ deps Deps }

func (r *runner) printf(format string, a ...any) { fmt.Fprintf(r.deps.Out, format, a...) }

func (r *runner) json(v any) error {
	enc := json.NewEncoder(r.deps.Out)
	return enc.Encode(v)
}

func (r *runner) confDir() (string, error) { return r.deps.ConfDir() }

// session is a logged-in machine: its login and a sync engine for it.
func (r *runner) session() (cloud.Credentials, *sync.Engine, error) {
	confDir, err := r.confDir()
	if err != nil {
		return cloud.Credentials{}, nil, err
	}
	creds, err := cloud.LoadCredentials(confDir)
	if err != nil {
		return creds, nil, err
	}
	return creds, &sync.Engine{Client: cloud.NewClient(creds.URL, creds.Token), ConfDir: confDir, Hooks: r.deps.Hooks, Now: r.deps.Now}, nil
}

// explain turns a revoked token into the message that says what to do.
func explain(err error) error {
	if apiErr, ok := cloud.AsAPIError(err); ok && apiErr.Unauthorized() {
		return fmt.Errorf("this device was disconnected from clai.chat (%s). Your local files are unchanged. To connect again: clai cloud login", apiErr.Message)
	}
	return err
}

func (r *runner) login(ctx context.Context, url, name string) error {
	confDir, err := r.confDir()
	if err != nil {
		return err
	}
	creds, _ := cloud.LoadCredentials(confDir)
	if url != "" {
		creds.URL = url
	}
	if name == "" {
		name, _ = r.deps.Hostname()
	}
	client := cloud.NewClient(creds.URL, "")
	flow := cloud.LoginFlow{
		Client: client, Now: r.deps.Now, Sleep: r.deps.Sleep,
		Show: func(code cloud.DeviceCode) error {
			r.printf("Open %s\nand approve the code %s for %q.\nWaiting for approval... (Ctrl-C to stop)\n", code.VerificationURIComplete, code.UserCode, name)
			return nil
		},
	}
	tok, err := flow.Run(ctx, name)
	if err != nil {
		return err
	}
	creds.Token, creds.Device, creds.Access, creds.ExpiresAt, creds.Account = tok.Key, name, tok.Access, tok.ExpiresAt, tok.Account
	if tok.Endpoint != "" {
		creds.URL = tok.Endpoint
	}
	client = cloud.NewClient(creds.URL, creds.Token)
	if me, err := client.Me(ctx); err == nil {
		creds.Account = me.Name()
	}
	// A login may be to another account: this machine's sync state is
	// forgotten, so the next pull starts from the server.
	if err := sync.ResetState(confDir); err != nil {
		return err
	}
	if err := cloud.SaveCredentials(confDir, creds); err != nil {
		return err
	}
	r.printf("\nLogged in as %s on %s.\nNothing is synced until you run one of:\n", orDash(creds.Account), hostOf(creds.URL))
	r.printf("  clai cloud status     what you would commit\n  clai cloud commit     upload your config\n  clai cloud pull       bring down your account's config\n")
	return nil
}

func hostOf(u string) string {
	u = strings.TrimPrefix(strings.TrimPrefix(u, "https://"), "http://")
	return strings.TrimRight(u, "/")
}

func (r *runner) logout(ctx context.Context) error {
	confDir, err := r.confDir()
	if err != nil {
		return err
	}
	creds, err := cloud.LoadCredentials(confDir)
	var notIn *cloud.NotLoggedInError
	if errors.As(err, &notIn) {
		r.printf("Not logged in.\n")
		return nil
	}
	if err != nil {
		return err
	}
	if err := cloud.NewClient(creds.URL, creds.Token).RevokeCurrent(ctx); err != nil {
		if apiErr, ok := cloud.AsAPIError(err); !ok || !apiErr.Unauthorized() {
			return fmt.Errorf("revoke the login on %s: %w", hostOf(creds.URL), err)
		}
	}
	if err := errors.Join(cloud.RemoveCredentials(confDir), sync.ResetState(confDir)); err != nil {
		return err
	}
	r.printf("Logged out. Your local files are unchanged.\n")
	return nil
}

func (r *runner) whoami(ctx context.Context, raw bool) error {
	creds, _, err := r.session()
	if err != nil {
		return err
	}
	me, err := cloud.NewClient(creds.URL, creds.Token).Me(ctx)
	if err != nil {
		return explain(err)
	}
	if raw {
		return r.json(me)
	}
	r.printf("Account: %s on %s\nDevice:  %s\n", me.Name(), hostOf(creds.URL), orDash(me.Device))
	r.printf("Access:  %s\n", orDash(me.Access))
	for _, s := range sync.Stores {
		info := me.Stores[string(s)]
		r.printf("%-14s %d files, %s, commit #%d\n", string(s)+":", info.Files, human(info.Bytes), info.Commit)
	}
	r.printf("Storage: %s of %s free\n", human(me.Usage.Bytes), human(me.Usage.FreeBytes))
	return nil
}

// stores reads a [store] argument; none means both.
func stores(args []string) ([]sync.Store, error) {
	if len(args) == 0 {
		return sync.Stores, nil
	}
	if len(args) > 1 {
		return nil, &UsageError{Msg: "name at most one store: config or conversations"}
	}
	s, ok := sync.ParseStore(args[0])
	if !ok {
		return nil, &UsageError{Msg: fmt.Sprintf("unknown store %q: use config or conversations", args[0])}
	}
	return []sync.Store{s}, nil
}

func (r *runner) status(ctx context.Context, args []string, imports, raw bool) error {
	which, err := stores(args)
	if err != nil {
		return err
	}
	_, engine, err := r.session()
	if err != nil {
		return err
	}
	var out []sync.Status
	for _, s := range which {
		st, err := engine.Status(ctx, s, sync.PlanOptions{Imports: imports})
		if err != nil {
			return err
		}
		if apiErr, ok := cloud.AsAPIError(st.RemoteError); ok && apiErr.Unauthorized() {
			return explain(apiErr)
		}
		out = append(out, st)
	}
	if raw {
		return r.json(out)
	}
	for _, st := range out {
		r.printStatus(st)
	}
	return nil
}

func (r *runner) printStatus(st sync.Status) {
	head := fmt.Sprintf("%-14s", st.Store)
	parts := []string{}
	if len(st.Changes) == 0 && len(st.Conflicts) == 0 {
		parts = append(parts, "clean")
	} else {
		parts = append(parts, fmt.Sprintf("%d local %s", len(st.Changes), plural(len(st.Changes), "change", "changes")))
	}
	switch {
	case st.RemoteError != nil:
		parts = append(parts, "server not reachable: "+st.RemoteError.Error())
	case st.Remote.Ahead > 0:
		parts = append(parts, fmt.Sprintf("server is %d %s ahead", st.Remote.Ahead, plural(int(st.Remote.Ahead), "commit", "commits")))
	}
	if !st.Pulled {
		parts = append(parts, "never pulled on this machine")
	}
	r.printf("%s %s\n", head, strings.Join(parts, " · "))
	both := map[string]bool{}
	if st.Remote != nil {
		for _, p := range st.Remote.Both {
			both[p] = true
		}
	}
	for _, p := range st.Conflicts {
		r.printf("  C %s   conflict: clai cloud resolve %s --mine|--theirs\n", p, p)
	}
	for _, c := range st.Changes {
		mark := map[sync.ChangeKind]string{sync.Added: "+", sync.Modified: "~", sync.Deleted: "-"}[c.Kind]
		note := ""
		if both[c.Path] {
			note = "   also changed on the server"
		}
		r.printf("  %s %s%s\n", mark, c.Path, note)
	}
	secrets, imports, large := 0, 0, 0
	for _, x := range st.Excluded {
		switch x.Kind {
		case sync.ExcludedSecret:
			if secrets == 0 {
				r.printf("  not synced, holds a secret:\n")
			}
			secrets++
			r.printf("    %s  %s\n", x.Path, x.Detail)
		case sync.ExcludedTooLarge:
			large++
			r.printf("  not synced, larger than %s: %s\n", x.Detail, x.Path)
		case sync.ExcludedImport:
			imports++
		}
	}
	if secrets > 0 {
		r.printf("    move the value to an env var or envfile, then commit\n")
	}
	if imports > 0 {
		r.printf("  %d imported %s from other tools not included; add them with commit -imports\n", imports, plural(imports, "file", "files"))
	}
}

func (r *runner) diff(ctx context.Context, args []string, remote bool) error {
	_, engine, err := r.session()
	if err != nil {
		return err
	}
	paths := args
	if len(paths) == 0 {
		for _, s := range sync.Stores {
			plan, err := engine.Plan(s, sync.PlanOptions{})
			if err != nil {
				return err
			}
			paths = append(paths, plan.Conflicts...)
			for _, c := range plan.Changes {
				paths = append(paths, c.Path)
			}
		}
	}
	if len(paths) == 0 {
		r.printf("No local changes.\n")
		return nil
	}
	for _, p := range paths {
		d, err := engine.Diff(ctx, p, remote)
		if err != nil {
			return explain(err)
		}
		r.printf("%s\n", d.Text())
	}
	return nil
}

func (r *runner) pull(ctx context.Context, args []string, theirs, mine, raw bool) error {
	if theirs && mine {
		return &UsageError{Msg: "-theirs and -mine cannot both be set"}
	}
	which, err := stores(args)
	if err != nil {
		return err
	}
	_, engine, err := r.session()
	if err != nil {
		return err
	}
	opts := sync.PullOptions{}
	switch {
	case theirs:
		opts.Mode = sync.ModeTheirs
	case mine:
		opts.Mode = sync.ModeMine
	}
	var results []sync.PullResult
	needsDecision := false
	interactive := r.deps.Interactive() && !raw
	for _, s := range which {
		if interactive {
			opts.Choose = func(f sync.FirstPull) (sync.Mode, error) { return r.chooseFirst(s, f) }
		}
		res, err := engine.Pull(ctx, s, opts)
		var first *sync.FirstPullError
		if errors.As(err, &first) {
			needsDecision = true
			if !raw {
				if opts.Choose == nil {
					r.printFirst(s, first.First)
				}
				r.printf("No answer: pull again with -theirs or -mine.\n")
			}
			results = append(results, res)
			continue
		}
		if err != nil {
			return explain(err)
		}
		results = append(results, res)
		needsDecision = needsDecision || len(res.Conflicts) > 0
		if !raw {
			r.printPull(res)
		}
	}
	if raw {
		if err := r.json(results); err != nil {
			return err
		}
	}
	if needsDecision {
		r.deps.SetExit(ExitDecisionNeeded)
	}
	return nil
}

func (r *runner) printFirst(store sync.Store, f sync.FirstPull) {
	r.printf("This is the first %s pull on this machine.\n", store)
	r.printf("Your account has %d %s files; this machine has %d.\n", f.Remote, store, f.Local)
	r.printf("  %d are only on the account   will be added\n", f.OnlyRemote)
	r.printf("  %d are the same              nothing to do\n", f.Same)
	r.printf("  %d differ: %s\n", len(f.Differ), strings.Join(f.Differ, ", "))
}

// chooseFirst asks a first pull's question on the terminal.
func (r *runner) chooseFirst(store sync.Store, f sync.FirstPull) (sync.Mode, error) {
	r.printFirst(store, f)
	r.printf("\nFor the %d that differ, use\n", len(f.Differ))
	r.printf("  [t] my account's copy  (this machine's go to cloud/trash/)\n")
	r.printf("  [m] this machine's copy (commit it to share it)\n")
	r.printf("  [c] decide one by one as conflicts\n  [q] quit\nChoice: ")
	line, err := bufio.NewReader(r.deps.In).ReadString('\n')
	if err != nil && line == "" {
		// Nobody answered, as with no terminal at all.
		return "", &sync.FirstPullError{First: f}
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "t":
		return sync.ModeTheirs, nil
	case "m":
		return sync.ModeMine, nil
	case "c":
		return sync.ModeConflicts, nil
	}
	return "", &QuitError{}
}

// QuitError is a first pull the user stopped at its question.
type QuitError struct{}

func (e *QuitError) Error() string { return "stopped: nothing was pulled" }

func (r *runner) printPull(res sync.PullResult) {
	parts := []string{fmt.Sprintf("Pulled %s #%d", res.Store, res.Commit)}
	for _, n := range []struct {
		count int
		word  string
	}{{res.Added, "added"}, {res.Updated, "updated"}, {res.Removed, "removed"}, {len(res.Trashed), "replaced"}} {
		if n.count > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n.count, n.word))
		}
	}
	if len(parts) == 1 {
		parts = append(parts, "up to date")
	}
	r.printf("%s\n", strings.Join(parts, " · "))
	if len(res.Merged) > 0 {
		r.printf("  %d merged: one copy only added messages to the other\n", len(res.Merged))
	}
	for _, f := range res.Forks {
		r.printf("  1 fork: %s was continued on both machines;\n    the other machine's copy is now %q (%s)\n", f.Path, f.Title, f.Forked)
	}
	if len(res.Trashed) > 0 {
		r.printf("  old copies kept 30 days in cloud/trash/\n")
	}
	if len(res.Conflicts) == 0 {
		return
	}
	r.printf("%d %s, your %s untouched:\n", len(res.Conflicts), plural(len(res.Conflicts), "conflict", "conflicts"), plural(len(res.Conflicts), "file is", "files are"))
	for _, p := range res.Conflicts {
		r.printf("  C %s\n    theirs saved to cloud/theirs/%s\n", p, p)
	}
	r.printf("See both:  clai cloud diff <path>\nSettle it: clai cloud resolve <path> --mine|--theirs\n")
}

// commitTargets splits commit's arguments into stores and paths.
func commitTargets(args []string) ([]sync.Store, map[sync.Store][]string, error) {
	paths := map[sync.Store][]string{}
	var which []sync.Store
	seen := map[sync.Store]bool{}
	add := func(s sync.Store) {
		if !seen[s] {
			seen[s] = true
			which = append(which, s)
		}
	}
	for _, a := range args {
		if s, ok := sync.ParseStore(a); ok {
			add(s)
			continue
		}
		s, ok := storeOfPrefix(a)
		if !ok {
			return nil, nil, &UsageError{Msg: fmt.Sprintf("%s is not a synced path", a)}
		}
		add(s)
		paths[s] = append(paths[s], a)
	}
	if len(which) == 0 {
		which = sync.Stores
	}
	return which, paths, nil
}

// storeOfPrefix accepts a file or a directory such as "profiles/".
func storeOfPrefix(p string) (sync.Store, bool) {
	if s, ok := sync.StoreOf(p); ok {
		return s, true
	}
	dir := strings.TrimSuffix(p, "/")
	if dir == "conversations" || strings.HasPrefix(dir, "conversations/") {
		return sync.StoreConversations, true
	}
	return sync.StoreOf(dir + "/x.json")
}

func (r *runner) commit(ctx context.Context, args []string, msg string, imports, raw bool) error {
	which, paths, err := commitTargets(args)
	if err != nil {
		return err
	}
	creds, engine, err := r.session()
	if err != nil {
		return err
	}
	if msg == "" {
		msg = "from " + orDash(creds.Device)
	}
	var results []sync.CommitResult
	decision := false
	for _, s := range which {
		res, err := engine.Commit(ctx, s, sync.CommitOptions{Message: msg, PlanOptions: sync.PlanOptions{Paths: paths[s], Imports: imports}})
		var open *sync.ConflictsOpenError
		var behind *sync.BehindError
		switch {
		case errors.As(err, &open):
			decision = true
			r.printf("Not committed. %d %s in %s first:\n", len(open.Paths), plural(len(open.Paths), "conflict needs resolving", "conflicts need resolving"), s)
			for _, p := range open.Paths {
				r.printf("  C %s\n", p)
			}
			continue
		case errors.As(err, &behind):
			decision = true
			r.printBehind(behind)
			continue
		case err != nil:
			return r.commitError(err)
		}
		results = append(results, res)
		if !raw {
			r.printCommit(res)
		}
	}
	if raw {
		if err := r.json(results); err != nil {
			return err
		}
	}
	if decision {
		r.deps.SetExit(ExitDecisionNeeded)
	}
	return nil
}

func (r *runner) commitError(err error) error {
	apiErr, ok := cloud.AsAPIError(err)
	if !ok {
		return err
	}
	switch apiErr.Code {
	case "card_required", "payment_overdue":
		if apiErr.URL != "" {
			return fmt.Errorf("%s: %s", apiErr.Message, apiErr.URL)
		}
	}
	return explain(err)
}

func (r *runner) printBehind(b *sync.BehindError) {
	r.printf("Not committed. The server changed %d of your %s files\nsince your last pull:\n", len(b.Paths), b.Store)
	for _, e := range b.Paths {
		detail := ""
		if e.Commit > 0 {
			detail = fmt.Sprintf("   #%d", e.Commit)
			if e.Message != "" {
				detail += fmt.Sprintf(" %q", e.Message)
			}
			detail += ", " + orDash(e.Device)
			if e.UpdatedAt > 0 {
				detail += ", " + ago(r.deps.Now(), time.UnixMilli(e.UpdatedAt))
			}
		}
		r.printf("  %s%s\n", e.Path, detail)
	}
	r.printf("Run clai cloud pull, then commit again.\n")
}

func (r *runner) printCommit(res sync.CommitResult) {
	if res.Files == 0 {
		r.printf("%-14s nothing to commit\n", res.Store)
	} else {
		extra := ""
		if res.Imported > 0 {
			extra = fmt.Sprintf(", %d imported", res.Imported)
		}
		r.printf("%s commit #%d · %d %s (%s)%s\n", res.Store, res.Commit, res.Files, plural(res.Files, "file", "files"), human(res.Bytes), extra)
	}
	for _, x := range res.Excluded {
		switch x.Kind {
		case sync.ExcludedSecret:
			r.printf("  not committed, holds a secret: %s  %s\n", x.Path, x.Detail)
		case sync.ExcludedTooLarge:
			r.printf("  not committed, larger than %s: %s\n", x.Detail, x.Path)
		}
	}
}

func (r *runner) resolve(args []string, mine, theirs bool) error {
	if len(args) != 1 {
		return &UsageError{Msg: "name one path: clai cloud resolve <path> [-mine|-theirs]"}
	}
	if mine && theirs {
		return &UsageError{Msg: "-theirs and -mine cannot both be set"}
	}
	_, engine, err := r.session()
	if err != nil {
		return err
	}
	how := sync.ResolveMine
	if theirs {
		how = sync.ResolveTheirs
	}
	res, err := engine.Resolve(args[0], how)
	if err != nil {
		return err
	}
	switch {
	case theirs:
		r.printf("Resolved %s with the server's file. Yours is in cloud/trash/.\n", res.Path)
	case mine:
		r.printf("Resolved %s with your file. Commit to share it.\n", res.Path)
	default:
		r.printf("Resolved %s with your edited file. Commit to share it.\n", res.Path)
	}
	return nil
}

func (r *runner) log(ctx context.Context, args []string, n int, raw bool) error {
	which, err := stores(args)
	if err != nil {
		return err
	}
	_, engine, err := r.session()
	if err != nil {
		return err
	}
	all := map[sync.Store][]sync.LogEntry{}
	for _, s := range which {
		entries, err := engine.Log(ctx, s, n)
		if err != nil {
			return explain(err)
		}
		all[s] = entries
	}
	if raw {
		return r.json(all)
	}
	for _, s := range which {
		r.printf("%s\n", s)
		if len(all[s]) == 0 {
			r.printf("  no commits yet\n")
		}
		for _, e := range all[s] {
			r.printf("  #%-5d %-28s %s · %d %s · %s\n", e.Commit, quoteOr(e.Message), orDash(e.Device), e.Files, plural(e.Files, "file", "files"), ago(r.deps.Now(), time.UnixMilli(e.CreatedAt)))
		}
	}
	return nil
}

func quoteOr(s string) string {
	if s == "" {
		return "-"
	}
	return fmt.Sprintf("%q", s)
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func human(n int64) string {
	switch {
	case n >= 1_000_000_000:
		return fmt.Sprintf("%.1f GB", float64(n)/1e9)
	case n >= 1_000_000:
		return fmt.Sprintf("%.1f MB", float64(n)/1e6)
	}
	return fmt.Sprintf("%.1f kB", float64(n)/1e3)
}

func ago(now, then time.Time) string {
	d := now.Sub(then)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%d min ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%d h ago", int(d.Hours()))
	}
	return then.Local().Format(time.DateOnly)
}
