package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/baalimago/clai/internal/board"
	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/utils"
	pub_models "github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
	"github.com/baalimago/go_away_boilerplate/pkg/table"
)

// summaryEstimateRunesPerToken divides the input cap for the confirmation's
// upper-bound token estimate.
const summaryEstimateRunesPerToken = 4

type summarizeOptions struct {
	since   time.Time
	force   bool
	yes     bool
	workers int
	model   string
}

type summarizeResult struct {
	id     string
	worker int
	chat   pub_models.Chat
	err    error
}

// summarizeProgress is the run's display: the plain printer (one line per
// result, JSON under -r) or the live board on a terminal.
type summarizeProgress interface {
	started(worker, job int, id string)
	finished(res summarizeResult)
	done(selected, labelled, failed int)
}

// summarizeJob is one queued conversation with its position in the
// selected set.
type summarizeJob struct {
	index int
	id    string
}

func (cq *ChatHandler) handleSummarize(ctx context.Context) error {
	if cq.summarizer == nil {
		return errors.New("chat summarize: no summarizer attached")
	}
	opts := cq.summarizeOptions
	if !opts.yes && !utils.Live {
		return errors.New("chat summarize: non-interactive mode needs -y to confirm the run")
	}
	rows, err := readChatIndex(cq.convDir)
	if err != nil {
		return fmt.Errorf("chat summarize: read chat index: %w", err)
	}
	selected := selectSummarizeRows(rows, opts.since, opts.force)
	if len(selected) == 0 {
		if cq.raw {
			cq.printSummarizeTotals(0, 0, 0)
		} else {
			fmt.Fprintf(cq.out, "0 conversations to summarize since %s\n", opts.since.Format(time.RFC3339))
		}
		return nil
	}
	if !opts.yes {
		ok, err := cq.confirmSummarize(len(selected))
		if err != nil {
			return err
		}
		if !ok {
			if cq.raw {
				cq.printJSONLine(map[string]bool{"aborted": true})
			} else {
				fmt.Fprintln(cq.out, "aborted")
			}
			return nil
		}
	}
	ids := make([]string, 0, len(selected))
	for _, row := range selected {
		ids = append(ids, row.ID)
	}
	progress := cq.newSummarizeProgress(ctx, len(ids))
	labelled, failed := cq.runSummarizeJobs(ctx, ids, progress)
	var errs []error
	if len(labelled) > 0 {
		if err := cq.flushSummarizeIndex(labelled); err != nil {
			ancli.Warnf("chat summarize: %v\n", err)
			errs = append(errs, err)
		}
	}
	progress.done(len(ids), len(labelled), failed)
	if failed > 0 {
		errs = append(errs, fmt.Errorf("chat summarize: %d of %d conversations failed", failed, len(ids)))
	}
	if err := ctx.Err(); err != nil {
		errs = append(errs, fmt.Errorf("chat summarize: cancelled: %w", err))
	}
	return errors.Join(errs...)
}

// selectSummarizeRows keeps the native rows updated inside the window that
// still lack a summary, or every such row when force is set (D15).
func selectSummarizeRows(rows []chatIndexRow, since time.Time, force bool) []chatIndexRow {
	var selected []chatIndexRow
	for _, row := range rows {
		if row.ID == "globalScope" || row.effectiveUpdated().Before(since) {
			continue
		}
		if row.Summary != "" && !force {
			continue
		}
		selected = append(selected, row)
	}
	return selected
}

func summarizeTokenEstimate(count int) int {
	return count * models.SummaryInputRunes / summaryEstimateRunesPerToken
}

func (cq *ChatHandler) confirmSummarize(count int) (bool, error) {
	fmt.Fprintf(cq.promptWriter(), "Summarize %d conversations since %s (at most ~%d input tokens)? [y/N]: ",
		count, cq.summarizeOptions.since.Format(time.RFC3339), summarizeTokenEstimate(count))
	choice, err := table.ReadUserInputFrom(cq.input)
	if err != nil {
		return false, fmt.Errorf("chat summarize: read confirmation: %w", err)
	}
	switch strings.ToLower(strings.TrimSpace(choice)) {
	case "y", "yes":
		return true, nil
	}
	return false, nil
}

// promptWriter is stdout, or stderr under -r so stdout carries only the
// JSON lines.
func (cq *ChatHandler) promptWriter() io.Writer {
	if !cq.raw {
		return cq.out
	}
	if cq.errOut != nil {
		return cq.errOut
	}
	return os.Stderr
}

// runSummarizeJobs drives the worker pool: one job channel fed until the
// context is done, one result channel drained by this coordinator, so every
// completed result is collected even when the run is cancelled.
func (cq *ChatHandler) runSummarizeJobs(ctx context.Context, ids []string, progress summarizeProgress) ([]pub_models.Chat, int) {
	workers := max(cq.summarizeOptions.workers, 1)
	jobs := make(chan summarizeJob)
	results := make(chan summarizeResult)
	var wg sync.WaitGroup
	for w := range workers {
		wg.Go(func() {
			for job := range jobs {
				progress.started(w, job.index, job.id)
				res := cq.summarizeOne(ctx, job.id)
				res.worker = w
				results <- res
			}
		})
	}
	go func() {
		defer close(jobs)
		for i, id := range ids {
			select {
			case jobs <- summarizeJob{index: i, id: id}:
			case <-ctx.Done():
				return
			}
		}
	}()
	go func() {
		wg.Wait()
		close(results)
	}()
	var labelled []pub_models.Chat
	failed := 0
	for res := range results {
		progress.finished(res)
		if res.err != nil {
			failed++
			continue
		}
		labelled = append(labelled, res.chat)
	}
	return labelled, failed
}

// summarizeOne runs one job on its own child of the command context, owning
// the cancel func under ContextCancelKey so a StopEvent-style cancel from
// the summarizer's runner reaches this job alone (D27).
func (cq *ChatHandler) summarizeOne(ctx context.Context, id string) summarizeResult {
	if err := ctx.Err(); err != nil {
		return summarizeResult{id: id, err: err}
	}
	jobCtx, jobCancel := context.WithCancel(ctx)
	defer jobCancel()
	jobCtx = context.WithValue(jobCtx, utils.ContextCancelKey, jobCancel)
	c, err := cq.getByID(id)
	if err != nil {
		return summarizeResult{id: id, err: fmt.Errorf("load: %w", err)}
	}
	s, err := cq.summarizer.Summarize(jobCtx, models.SummaryRequest{Chat: c, Model: cq.summarizeOptions.model})
	if err != nil {
		return summarizeResult{id: id, err: err}
	}
	s.ApplyTo(&c)
	if err := SaveWithoutIndex(cq.convDir, c); err != nil {
		return summarizeResult{id: id, err: fmt.Errorf("save: %w", err)}
	}
	return summarizeResult{id: id, chat: c}
}

func (cq *ChatHandler) flushSummarizeIndex(labelled []pub_models.Chat) error {
	upsert := cq.upsertIndexBatch
	if upsert == nil {
		upsert = UpsertChatIndexBatch
	}
	if err := upsert(cq.convDir, labelled); err != nil {
		return fmt.Errorf("update chat index: %w", err)
	}
	return nil
}

// summarizeLive reports whether the run draws the board: an interactive
// run on a terminal, never under -r or -n.
func (cq *ChatHandler) summarizeLive() bool {
	return cq.forceLive || (!cq.raw && utils.Live && utils.IsTerminalWriter(cq.out))
}

func (cq *ChatHandler) newSummarizeProgress(ctx context.Context, selected int) summarizeProgress {
	if !cq.summarizeLive() {
		return plainSummarizeProgress{cq: cq}
	}
	return newBoardSummarizeProgress(ctx, cq, selected)
}

// plainSummarizeProgress is today's output: one line per result and a
// totals line, JSON objects under -r.
type plainSummarizeProgress struct{ cq *ChatHandler }

func (p plainSummarizeProgress) started(int, int, string) {}

func (p plainSummarizeProgress) finished(res summarizeResult) { p.cq.printSummarizeResult(res) }

func (p plainSummarizeProgress) done(selected, labelled, failed int) {
	p.cq.printSummarizeTotals(selected, labelled, failed)
}

// Board columns of the summarize table, in Row.Cells order.
const (
	sumColConversation = iota
	sumColState
	sumColModel
	sumColTokens
)

// Summarize board layout: the position column shows the job's index in
// the selected set, the conversation column takes what the terminal
// leaves after the fixed columns, between a truncated and a full UUID.
const (
	sumStateWidth   = 12 // "summarizing"
	sumModelWidth   = 14
	sumTokensWidth  = 6
	sumTimeWidth    = 7 // "12m34s"
	sumConvMin      = 12
	sumConvMax      = 36 // a UUID
	sumFixedColumns = 2 + 5 + 2 + 2 + sumStateWidth + 2 + sumModelWidth + 2 + sumTokensWidth + 2 + sumTimeWidth
)

// summarizeConversationWidth sizes the conversation column for a terminal
// of the given width; zero means unknown and takes the dimensions fallback.
func summarizeConversationWidth(width int) int {
	if width <= 0 {
		width = utils.SessionDimensions(nil).Width
	}
	return min(max(width-sumFixedColumns-2, sumConvMin), sumConvMax)
}

// boardSummarizeProgress draws one row per worker on the shared progress
// board, logs every finished conversation above it and keeps a live
// footer with totals and an estimate.
type boardSummarizeProgress struct {
	b      *board.Board
	cancel context.CancelFunc
	begun  time.Time
	now    func() time.Time

	mu         sync.Mutex
	selected   int
	labelled   int
	failed     int
	spent      time.Duration // wall time of finished jobs, for the estimate
	workers    int
	inProgress map[int]time.Time
}

func newBoardSummarizeProgress(ctx context.Context, cq *ChatHandler, selected int) *boardSummarizeProgress {
	workers := max(cq.summarizeOptions.workers, 1)
	rows := make([]board.Row, workers)
	for i := range rows {
		rows[i] = board.Row{Index: board.MarkPending, Cells: []string{board.MarkPending, "idle", board.MarkPending, board.MarkPending}, Mark: board.MarkPending}
	}
	model := cq.summarizeOptions.model
	if model == "" {
		model = "ladder"
	}
	width := cq.boardWidth
	if width <= 0 {
		width = utils.SessionDimensions(cq.out).Width
	}
	title := fmt.Sprintf("summarizing  %d conversations since %s", selected, cq.summarizeOptions.since.Format("2006-01-02 15:04"))
	b := board.New(cq.out, true, board.Config{
		Title: title,
		Index: "slot",
		Columns: []board.Column{
			{Name: "conversation", Width: summarizeConversationWidth(width)},
			{Name: "state", Width: sumStateWidth, Mark: true},
			{Name: "model", Width: sumModelWidth},
			{Name: "tokens", Width: sumTokensWidth},
		},
		Time: "time",
	}, rows)
	b.SetWidth(width)
	p := &boardSummarizeProgress{b: b, begun: time.Now(), now: time.Now, selected: selected, workers: workers, inProgress: map[int]time.Time{}}
	if cq.now != nil {
		p.now = cq.now
		p.begun = cq.now()
		b.SetClock(cq.now)
	}
	b.SetFooterFunc(p.footer)
	b.SetPhase(fmt.Sprintf("summarizing · %d conversations · %d workers · model %s", selected, workers, model))
	animCtx, cancel := context.WithCancel(ctx)
	p.cancel = cancel
	go b.Animate(animCtx)
	return p
}

func (p *boardSummarizeProgress) started(worker, job int, id string) {
	p.mu.Lock()
	p.inProgress[worker] = p.now()
	total := p.selected
	p.mu.Unlock()
	p.b.Update(worker, func(r *board.Row) {
		r.Index = fmt.Sprintf("%d/%d", job+1, total)
		r.Active, r.Started, r.Elapsed, r.Mark = true, p.now(), 0, board.MarkPending
		r.Cells[sumColConversation], r.Cells[sumColState], r.Cells[sumColModel], r.Cells[sumColTokens] = id, "summarizing", board.MarkPending, board.MarkPending
	})
}

func (p *boardSummarizeProgress) finished(res summarizeResult) {
	p.mu.Lock()
	elapsed := time.Duration(0)
	if at, ok := p.inProgress[res.worker]; ok {
		elapsed = p.now().Sub(at)
		delete(p.inProgress, res.worker)
	}
	p.spent += elapsed
	if res.err != nil {
		p.failed++
	} else {
		p.labelled++
	}
	p.mu.Unlock()
	p.b.Update(res.worker, func(r *board.Row) {
		r.Active, r.Elapsed = false, elapsed
		if res.err != nil {
			r.Mark, r.Cells[sumColState] = board.MarkWarn, "failed"
			return
		}
		r.Mark, r.Cells[sumColState] = board.MarkDone, "labelled"
		r.Cells[sumColModel], r.Cells[sumColTokens] = summaryModelOf(res.chat), summaryTokensOf(res.chat)
	})
	if res.err != nil {
		p.b.Log(fmt.Sprintf("%s %s  %v", board.MarkWarn, res.id, res.err))
		return
	}
	p.b.Log(fmt.Sprintf("%s %s  %s", board.MarkDone, res.id, res.chat.Title))
}

func (p *boardSummarizeProgress) done(selected, labelled, failed int) {
	p.cancel()
	p.b.SetPhase(fmt.Sprintf("done · %d labelled · %d failed · %s", labelled, failed, p.now().Sub(p.begun).Round(time.Second)))
	p.b.Finish(fmt.Sprintf("summarized %d of %d conversations, %d failed", labelled, selected, failed))
}

// footer renders totals and an estimate: mean wall time per finished job,
// times the jobs still to come, spread over the workers.
func (p *boardSummarizeProgress) footer(rows []board.Row) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	finished := p.labelled + p.failed
	remaining := p.selected - finished
	eta := "eta ·"
	if finished > 0 && remaining > 0 {
		mean := p.spent / time.Duration(finished)
		per := max(p.workers, 1)
		eta = "eta ~" + (mean * time.Duration((remaining+per-1)/per)).Round(time.Second).String()
	}
	return fmt.Sprintf("labelled %d/%d · failed %d · in flight %d · %s elapsed · %s",
		p.labelled, p.selected, p.failed, board.InFlight(rows), p.now().Sub(p.begun).Round(time.Second), eta)
}

// summaryModelOf is the model of the newest summary row.
func summaryModelOf(c pub_models.Chat) string {
	for i := len(c.Queries) - 1; i >= 0; i-- {
		if c.Queries[i].Purpose == "summary" && c.Queries[i].Model != "" {
			return c.Queries[i].Model
		}
	}
	return board.MarkPending
}

// summaryTokensOf sums the tokens of the newest summary rows appended by
// this run (every summary row: a -force run appends more, and the column
// is a hint, not an invoice).
func summaryTokensOf(c pub_models.Chat) string {
	total := 0
	for _, q := range c.Queries {
		if q.Purpose == "summary" {
			total += q.Usage.TotalTokens
		}
	}
	if total == 0 {
		return board.MarkPending
	}
	return fmt.Sprint(total)
}

func (cq *ChatHandler) printSummarizeResult(res summarizeResult) {
	if cq.raw {
		line := map[string]string{"id": res.id, "title": res.chat.Title, "summary": res.chat.Summary, "error": ""}
		if res.err != nil {
			line["error"] = res.err.Error()
		}
		cq.printJSONLine(line)
		return
	}
	if res.err != nil {
		fmt.Fprintf(cq.out, "%s: ERROR %v\n", res.id, res.err)
		return
	}
	fmt.Fprintf(cq.out, "%s: %s\n", res.id, res.chat.Title)
}

func (cq *ChatHandler) printSummarizeTotals(selected, labelled, failed int) {
	if cq.raw {
		cq.printJSONLine(map[string]int{"selected": selected, "labelled": labelled, "failed": failed})
		return
	}
	fmt.Fprintf(cq.out, "summarized %d of %d conversations, %d failed\n", labelled, selected, failed)
}

func (cq *ChatHandler) printJSONLine(v any) {
	b, err := json.Marshal(v)
	if err != nil {
		ancli.Warnf("chat summarize: encode output line: %v\n", err)
		return
	}
	fmt.Fprintf(cq.out, "%s\n", b)
}
