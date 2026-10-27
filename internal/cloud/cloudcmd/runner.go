package cloudcmd

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	stdsync "sync"
	"time"

	"github.com/baalimago/clai/internal/board"
	"github.com/baalimago/clai/internal/cloud"
	"github.com/baalimago/clai/internal/cloud/sync"
)

type runner struct {
	deps Deps
	// creds is the login session loaded, for explain.
	creds cloud.Credentials
}

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
	r.creds = creds
	if err != nil {
		return creds, nil, err
	}
	return creds, &sync.Engine{Client: cloud.NewClient(creds.URL, creds.Token), ConfDir: confDir, Hooks: r.deps.Hooks, Now: r.deps.Now}, nil
}

func (r *runner) login(ctx context.Context, url, name string) error {
	confDir, err := r.confDir()
	if err != nil {
		return err
	}
	creds, err := cloud.LoadCredentials(confDir)
	if insecure := (*cloud.InsecureURLError)(nil); errors.As(err, &insecure) && url == "" {
		return err
	}
	if creds.URL == "" {
		creds.URL = cloud.DefaultURL
	}
	if url != "" {
		creds.URL = url
	}
	if err := cloud.CheckURL(creds.URL); err != nil {
		return err
	}
	if name == "" {
		name, _ = r.deps.Hostname()
	}
	client := cloud.NewClient(creds.URL, "")
	flow := cloud.LoginFlow{
		Client: client, Now: r.deps.Now, Sleep: r.deps.Sleep,
		Show: func(code cloud.DeviceCode) error {
			r.lines(approveLines(code, name, r.style()))
			return nil
		},
	}
	tok, err := flow.Run(ctx, name)
	if err != nil {
		return r.explain(err)
	}
	creds.Token, creds.Device, creds.Access, creds.ExpiresAt, creds.Account = tok.Key, name, tok.Access, tok.ExpiresAt, tok.Account
	if tok.Endpoint != "" {
		if err := cloud.CheckURL(tok.Endpoint); err != nil {
			return fmt.Errorf("the login answered with another server: %w", err)
		}
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
	r.lines(loginLines(orDash(creds.Account), hostOf(creds.URL), r.style()))
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
	r.creds = creds
	var notIn *cloud.NotLoggedInError
	if errors.As(err, &notIn) {
		r.lines([]string{r.style().Head("not logged in")})
		return nil
	}
	if err != nil {
		return err
	}
	if err := cloud.NewClient(creds.URL, creds.Token).RevokeCurrent(ctx); err != nil {
		if apiErr, ok := cloud.AsAPIError(err); !ok || !apiErr.Unauthorized() {
			return r.explain(err)
		}
	}
	if err := errors.Join(cloud.RemoveCredentials(confDir), sync.ResetState(confDir)); err != nil {
		return err
	}
	r.lines([]string{r.style().Head("logged out", "your local files are unchanged")})
	return nil
}

func (r *runner) whoami(ctx context.Context, raw bool) error {
	creds, _, err := r.session()
	if err != nil {
		return err
	}
	me, err := cloud.NewClient(creds.URL, creds.Token).Me(ctx)
	if err != nil {
		return r.explain(err)
	}
	if raw {
		return r.json(me)
	}
	r.lines(whoamiLines(me, hostOf(creds.URL), r.style()))
	return nil
}

const gigabyte = 1_000_000_000

func billingLine(me cloud.Me) string {
	line := fmt.Sprintf("billing: config sync is free (%s of %s); ",
		human(me.Stores[string(sync.StoreConfig)].Bytes), human(me.Usage.ConfigLimitBytes))
	if !me.Usage.CardOnFile {
		return line + "conversations need a card on file, $1 per started GB a month"
	}
	held := me.Stores[string(sync.StoreConversations)].Bytes
	line += fmt.Sprintf("conversations $%d a month at %s", (held+gigabyte-1)/gigabyte, human(held))
	if me.Usage.Overdue {
		line += "; a payment is overdue, so commits are refused"
	}
	return line
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
		status := r.track(engine, "checking "+string(s))
		st, err := engine.Status(ctx, s, sync.PlanOptions{Imports: imports})
		status.Stop()
		if err != nil {
			return r.explain(err)
		}
		if apiErr, ok := cloud.AsAPIError(st.RemoteError); ok && apiErr.Unauthorized() {
			return r.explain(apiErr)
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

func (r *runner) style() style { return style{Color: r.deps.Color()} }

func (r *runner) lines(lines []string) {
	for _, l := range lines {
		r.printf("%s\n", l)
	}
}

func (r *runner) printStatus(st sync.Status) { r.lines(statusLines(st, r.style())) }

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
		r.lines([]string{r.style().Head("diff", "no local changes")})
		return nil
	}
	for _, p := range paths {
		d, err := engine.Diff(ctx, p, remote)
		if err != nil {
			return r.explain(err)
		}
		r.lines(diffLines(d, r.style()))
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
	in := bufio.NewReader(r.deps.In)
	for _, s := range which {
		status := r.track(engine, "pulling "+string(s))
		if interactive {
			opts.Choose = func(f sync.FirstPull) (sync.Mode, error) {
				// The question needs the terminal row the status draws on.
				status.Stop()
				defer status.Start()
				return r.chooseFirst(s, f, in)
			}
		}
		res, err := engine.Pull(ctx, s, opts)
		status.Stop()
		var quit *QuitError
		if errors.As(err, &quit) {
			// A deliberate stop: the question is still open, and no other
			// store is pulled behind the user's back.
			r.printf("  %s\n", r.style().Secondary(quit.Error()))
			r.deps.SetExit(ExitDecisionNeeded)
			return nil
		}
		var first *sync.FirstPullError
		if errors.As(err, &first) {
			needsDecision = true
			if !raw {
				if opts.Choose == nil {
					r.lines(firstPullLines(s, first.First, r.style()))
				}
				r.printf("  %s\n", r.style().Secondary("not pulled: no answer, so nothing changed"))
				r.lines(firstPullFlags(s, r.style()))
			}
			results = append(results, res)
			continue
		}
		if err != nil {
			return r.explain(err)
		}
		results = append(results, res)
		needsDecision = needsDecision || len(res.Conflicts) > 0
		if !raw {
			r.lines(pullLines(res, r.style()))
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

// chooseFirst asks a first pull's question on the terminal, again until
// the answer is one of the choices.
func (r *runner) chooseFirst(store sync.Store, f sync.FirstPull, in *bufio.Reader) (sync.Mode, error) {
	r.lines(firstPullLines(store, f, r.style()))
	r.lines(firstPullChoices(store, len(f.Differ), r.style()))
	for {
		r.printf("  Keep [a/h/d/q]: ")
		line, err := in.ReadString('\n')
		if err != nil && line == "" {
			// Nobody answered, as with no terminal at all.
			r.printf("\n")
			return "", &sync.FirstPullError{First: f}
		}
		switch choice, ok := parseChoice(line); {
		case ok && choice.quit:
			return "", &QuitError{}
		case ok:
			return choice.mode, nil
		}
		r.printf("  %s\n", r.style().Secondary("answer a, h, d or q"))
	}
}

// QuitError is a first pull the user stopped at its question.
type QuitError struct{}

func (e *QuitError) Error() string { return "stopped: nothing was pulled" }

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

// commitOutcome is what became of one store in a commit.
type commitOutcome string

const (
	commitCommitted commitOutcome = "committed"
	commitUpToDate  commitOutcome = "up_to_date"
	commitBehind    commitOutcome = "behind"
	commitConflicts commitOutcome = "conflicts"
	commitBilling   commitOutcome = "billing"
)

// commitReport is one store of commit -r: the engine's result plus what
// stopped it, if anything.
type commitReport struct {
	sync.CommitResult
	Status    commitOutcome      `json:"status"`
	Conflicts []string           `json:"conflicts,omitempty"`
	Behind    []sync.RemoteEntry `json:"behind,omitempty"`
	Billing   *billingReport     `json:"billing,omitempty"`
}

type billingReport struct {
	Reason sync.BillingReason `json:"reason"`
	URL    string             `json:"url,omitempty"`
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
		msg = "from " + r.deviceName(creds)
	}
	var reports []commitReport
	decision := false
	for _, s := range which {
		status := r.track(engine, "committing "+string(s))
		res, err := engine.Commit(ctx, s, sync.CommitOptions{Message: msg, PlanOptions: sync.PlanOptions{Paths: paths[s], Imports: imports}})
		status.Stop()
		res.Store = s
		if res.Excluded == nil {
			res.Excluded = []sync.Excluded{}
		}
		if res.Changes == nil {
			res.Changes = []sync.LocalChange{}
		}
		report := commitReport{CommitResult: res, Status: commitCommitted}
		var lines []string
		var open *sync.ConflictsOpenError
		var behind *sync.BehindError
		var blocked *sync.BillingBlockedError
		switch {
		case errors.As(err, &blocked) && len(args) == 0:
			// A plain commit still commits the free config store.
			report.Status, report.Billing = commitBilling, &billingReport{Reason: blocked.Reason, URL: blocked.URL}
			decision = true
			lines = blockedLines(s, blocked, r.style())
		case errors.As(err, &open):
			decision = true
			report.Status, report.Conflicts = commitConflicts, open.Paths
			lines = conflictsOpenLines(open, r.style())
		case errors.As(err, &behind):
			decision = true
			report.Status, report.Behind = commitBehind, behind.Paths
			lines = behindLines(behind, r.deps.Now(), r.style())
		case err != nil:
			return r.commitError(err)
		default:
			if res.Files == 0 {
				report.Status = commitUpToDate
			}
			lines = commitLines(res, r.style())
		}
		reports = append(reports, report)
		if !raw {
			r.lines(lines)
		}
	}
	if raw {
		if err := r.json(reports); err != nil {
			return err
		}
	}
	if decision {
		r.deps.SetExit(ExitDecisionNeeded)
	}
	return nil
}

// deviceName names this machine in a default commit message: the login's
// device, else the hostname, as with a CLAI_CLOUD_TOKEN.
func (r *runner) deviceName(creds cloud.Credentials) string {
	if creds.Device != "" {
		return creds.Device
	}
	if host, err := r.deps.Hostname(); err == nil && host != "" {
		return host
	}
	return "-"
}

// track shows a status line on a terminal's stderr while engine works,
// with what it is doing as the detail. Stop it before printing.
func (r *runner) track(engine *sync.Engine, title string) *board.Status {
	status := board.NewStatus(r.deps.Err, r.deps.ErrIsTerminal(), title)
	var mu stdsync.Mutex
	var phase sync.Progress
	var since time.Time
	engine.Progress = func(p sync.Progress) {
		mu.Lock()
		defer mu.Unlock()
		now := r.deps.Now()
		if p.Phase != phase.Phase || p.Store != phase.Store {
			phase, since = p, now
		}
		status.SetDetail(progressText(p, now.Sub(since)))
	}
	status.Start()
	return status
}

// paceAfter is how long a phase runs before its pace is worth showing.
const paceAfter = 2 * time.Second

func progressText(p sync.Progress, elapsed time.Duration) string {
	switch p.Phase {
	case sync.PhaseScanning:
		return "found " + count(p.Done) + " " + plural(p.Done, "file", "files")
	case sync.PhaseDiscovering:
		return "looking for conversations in other tools"
	case sync.PhaseExporting:
		if p.Total == 0 {
			return "exporting conversations from other tools"
		}
		return "exporting from other tools " + count(p.Done) + "/" + count(p.Total) + pace(p, elapsed)
	case sync.PhaseHashing:
		if p.Total == 0 {
			return count(p.Reused) + " " + plural(p.Reused, "file", "files") + " unchanged since the last run"
		}
		return fmt.Sprintf("reading %s/%s changed files · %s of %s · %s unchanged", count(p.Done), count(p.Total), human(p.Bytes), human(p.TotalBytes), count(p.Reused)) + pace(p, elapsed)
	case sync.PhaseUploading:
		return fmt.Sprintf("uploaded %d/%d files · %s of %s", p.Done, p.Total, human(p.Bytes), human(p.TotalBytes)) + pace(p, elapsed)
	case sync.PhaseDownloading:
		if p.Total == 0 {
			return "all " + count(p.Have) + " " + plural(p.Have, "file", "files") + " already here"
		}
		line := fmt.Sprintf("downloaded %s/%s files · %s of %s", count(p.Done), count(p.Total), human(p.Bytes), human(p.TotalBytes))
		if p.Have > 0 {
			line += " · " + count(p.Have) + " already here"
		}
		if p.Copies > 0 {
			line += " · " + count(p.Copies) + " " + plural(p.Copies, "copy", "copies")
		}
		return line + pace(p, elapsed)
	case sync.PhaseIndexing:
		return "indexing " + count(p.Done) + "/" + count(p.Total) + " pulled chats" + pace(p, elapsed)
	case sync.PhaseCommitting:
		return "recording the commit"
	}
	return fmt.Sprintf("%d/%d files", p.Done, p.Total)
}

// pace is " · <rate> · <time> left", by bytes when the phase counts them
// and by files otherwise; empty until the phase has run long enough to tell.
// Time left is the slower of the bytes' and the files' estimates, since a
// pull of many small files is bound by requests, not bandwidth.
func pace(p sync.Progress, elapsed time.Duration) string {
	if elapsed < paceAfter || p.Done == 0 || p.Total == 0 {
		return ""
	}
	secs := elapsed.Seconds()
	files := float64(p.Done) / secs
	left := float64(p.Total-p.Done) / files
	if p.TotalBytes > 0 && p.Bytes > 0 {
		rate := float64(p.Bytes) / secs
		return " · " + human(int64(rate)) + "/s · " + remaining(max(left, float64(p.TotalBytes-p.Bytes)/rate)) + " left"
	}
	perSec := fmt.Sprintf("%.0f", files)
	if files < 10 {
		perSec = fmt.Sprintf("%.1f", files)
	}
	return " · " + perSec + " files/s · " + remaining(left) + " left"
}

func remaining(secs float64) string {
	d := time.Duration(secs) * time.Second
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
}

func (r *runner) commitError(err error) error {
	apiErr, ok := cloud.AsAPIError(err)
	if !ok {
		return r.explain(err)
	}
	switch apiErr.Code {
	case "card_required", "payment_overdue":
		if apiErr.URL != "" {
			return fmt.Errorf("%s: %s", apiErr.Message, apiErr.URL)
		}
	}
	return r.explain(err)
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
	kept := resolvedEdited
	switch {
	case theirs:
		kept = resolvedTheirs
	case mine:
		kept = resolvedMine
	}
	r.lines(resolveLines(res, kept, r.style()))
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
			return r.explain(err)
		}
		all[s] = entries
	}
	if raw {
		return r.json(all)
	}
	for _, s := range which {
		r.lines(logLines(s, all[s], r.deps.Now(), r.style()))
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

// human is a size in SI units with one decimal, and in bytes below 1 kB.
func human(n int64) string { return sync.HumanBytes(n) }

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
