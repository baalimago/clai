package audio

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/baalimago/clai/internal/board"
	"github.com/baalimago/clai/internal/capture"
	"github.com/baalimago/clai/internal/diag"
	"github.com/baalimago/clai/internal/display"
	"github.com/baalimago/clai/internal/utils"
)

// recordingPlace names the directory a recording lands in and every stop that
// can end it (architecture/audio.md, capture pipeline).
func recordingPlace(dir string, cfg CaptureConfig, enterStops bool) string {
	stops := make([]string, 0, 3)
	if enterStops {
		stops = append(stops, "[Enter]")
	}
	if cfg.SilenceTimeout > 0 {
		stops = append(stops, stopText(time.Duration(cfg.SilenceTimeout)*time.Second)+" silence")
	}
	if cfg.MaxSeconds > 0 {
		stops = append(stops, "max "+stopText(time.Duration(cfg.MaxSeconds)*time.Second))
	}
	if len(stops) == 0 {
		return dir
	}
	return dir + " · stops: " + strings.Join(stops, ", ")
}

// stopText renders a stop threshold without zero parts: 45s, 1m30s, 10m,
// 1h30m, 2h.
func stopText(d time.Duration) string {
	d = d.Round(time.Second)
	var text strings.Builder
	if hours := int(d.Hours()); hours > 0 {
		fmt.Fprintf(&text, "%dh", hours)
	}
	if minutes := int(d.Minutes()) % 60; minutes > 0 {
		fmt.Fprintf(&text, "%dm", minutes)
	}
	if seconds := int(d.Seconds()) % 60; seconds > 0 || text.Len() == 0 {
		fmt.Fprintf(&text, "%ds", seconds)
	}
	return text.String()
}

// dbText renders a level without a trailing zero: -40, -40.5.
func dbText(db float64) string {
	return strconv.FormatFloat(db, 'f', -1, 64)
}

// recordProgressTitle names the progress records of a recording under -json.
const recordProgressTitle = "audio_record"

// recordNotice is the row shown while a recording runs. A terminal gets one
// live row that erases itself; any other writer gets one plain line; a JSON
// Displayer gets one progress record per state change.
type recordNotice struct {
	mu    sync.Mutex
	out   io.Writer
	disp  display.Displayer
	emit  sync.Mutex // orders progress records; guards the fields below
	begun bool
	ended bool
	// early holds notes shown before start, reported right after it.
	early  []string
	place  string
	live   bool
	cfg    CaptureConfig
	status *board.Status
	now    func() time.Time
	state  capture.CaptureState
	since  time.Time
	stop   chan struct{}
	done   chan struct{}
}

func newRecordNotice(out io.Writer, live bool, cfg CaptureConfig, place string) *recordNotice {
	notice := &recordNotice{
		out:    out,
		place:  place,
		live:   live,
		cfg:    cfg,
		status: board.NewStatus(out, live, "recording"),
		now:    time.Now,
	}
	notice.status.SetDetail(notice.detail(notice.now()))
	return notice
}

// newJSONRecordNotice reports the recording as progress records through disp
// and never draws.
func newJSONRecordNotice(disp display.Displayer, cfg CaptureConfig, place string) *recordNotice {
	notice := newRecordNotice(io.Discard, false, cfg, place)
	notice.disp = disp
	return notice
}

func (n *recordNotice) progress(state, detail string) error {
	n.emit.Lock()
	defer n.emit.Unlock()
	if n.ended {
		return nil
	}
	if !n.begun && state == "update" {
		n.early = append(n.early, detail)
		return nil
	}
	n.begun = true
	n.ended = state == "done" || state == "failed"
	if err := n.report(state, detail); err != nil {
		return err
	}
	if state != "start" {
		return nil
	}
	for _, line := range n.early {
		if err := n.report("update", line); err != nil {
			return err
		}
	}
	n.early = nil
	return nil
}

func (n *recordNotice) report(state, detail string) error {
	if err := n.disp.ReportProgress(display.Progress{Title: recordProgressTitle, State: state, Detail: detail}); err != nil {
		return diag.Errorf("report recording progress: %w", err)
	}
	return nil
}

// detail is the text after the elapsed clock: the silence state, the threshold
// that decides it, and where the recording lands.
func (n *recordNotice) detail(now time.Time) string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.detailLocked(now)
}

func (n *recordNotice) detailLocked(now time.Time) string {
	parts := make([]string, 0, 3)
	if n.state != "" {
		parts = append(parts, n.stateText(now))
	}
	parts = append(parts, "threshold "+dbText(n.cfg.SilencePeakDB)+" dB")
	return strings.Join(append(parts, n.place), " · ")
}

// stateText is the silence state, with the silence stop's progress while the
// input is silent.
func (n *recordNotice) stateText(now time.Time) string {
	if n.state == capture.StateSound {
		return "▮ sound"
	}
	text := "▁ silent " + clock(now.Sub(n.since))
	if n.cfg.SilenceTimeout > 0 {
		text += " / " + stopText(time.Duration(n.cfg.SilenceTimeout)*time.Second)
	}
	return text
}

// setState applies one capture event; it runs on the capture reader goroutine
// and must not block.
func (n *recordNotice) setState(state capture.CaptureState) {
	n.mu.Lock()
	changed := state != n.state
	if changed {
		n.since = n.now()
	}
	n.state = state
	if n.disp != nil {
		if changed {
			_ = n.progress("update", string(state)+" · threshold "+dbText(n.cfg.SilencePeakDB)+" dB")
		}
		n.mu.Unlock()
		return
	}
	detail := n.detailLocked(n.now())
	n.mu.Unlock()
	n.status.SetDetail(detail)
}

func (n *recordNotice) start() error {
	if n.disp != nil {
		return n.progress("start", n.place)
	}
	if !n.live {
		if _, err := fmt.Fprintf(n.out, "recording %s\n", n.place); err != nil {
			return diag.Errorf("print recording notice: %w", err)
		}
		return nil
	}
	n.status.Start()
	stop, done := make(chan struct{}), make(chan struct{})
	n.mu.Lock()
	n.stop, n.done = stop, done
	n.mu.Unlock()
	go n.tick(stop, done)
	return nil
}

// tick keeps the silence clock and the threshold visible between capture
// events.
func (n *recordNotice) tick(stop, done chan struct{}) {
	defer close(done)
	ticker := time.NewTicker(board.TickInterval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			n.status.SetDetail(n.detail(n.now()))
		}
	}
}

// finish ends the notice. A cancelled recording that was kept is named on its
// own line, because the run it belongs to is ending and may show no tool
// result: under JSON it ends done with that line as detail, any other error
// ends failed.
func (n *recordNotice) finish(runErr error) error {
	var interrupted *capture.InterruptedError
	saved := errors.As(runErr, &interrupted) && interrupted.Path != ""
	if n.disp != nil {
		switch {
		case saved:
			return n.progress("done", runErr.Error())
		case runErr != nil:
			return n.progress("failed", runErr.Error())
		}
		return n.progress("done", "")
	}
	n.stopNotice()
	if !saved {
		return nil
	}
	if _, err := fmt.Fprintln(n.out, runErr.Error()); err != nil {
		return diag.Errorf("print saved recording: %w", err)
	}
	return nil
}

func (n *recordNotice) stopNotice() {
	n.mu.Lock()
	stop, done := n.stop, n.done
	n.stop, n.done = nil, nil
	n.mu.Unlock()
	if stop != nil {
		close(stop)
		<-done
	}
	n.status.Stop()
}

// note prints one line above the row, which the next frame redraws below it.
// askCalibration asks the user to acknowledge this machine's silence levels:
// a calibrate prompt record under -json, the notice line otherwise.
func (n *recordNotice) askCalibration() error {
	if n.disp == nil {
		return n.note(calibrationNotice)
	}
	if err := n.disp.ShowPrompt(display.Prompt{Kind: "calibrate", Text: calibrationQuestion, Choices: []string{"a"}}); err != nil {
		return diag.Errorf("show calibration question: %w", err)
	}
	return nil
}

func (n *recordNotice) note(line string) error {
	if n.disp != nil {
		return n.progress("update", line)
	}
	prefix := ""
	if n.live {
		prefix = "\r\x1b[2K"
	}
	if _, err := fmt.Fprintf(n.out, "%s%s\n", prefix, line); err != nil {
		return diag.Errorf("print recording note: %w", err)
	}
	return nil
}

// acknowledge records that the user accepted this machine's silence levels and
// reports the outcome on the row. A failed store is displayed, never fatal: a
// meeting must not be lost because a convenience flag could not be written.
func (n *recordNotice) acknowledge() {
	err := storeCalibration()
	line := "silence calibration acknowledged"
	if err != nil {
		line = "silence calibration not stored: " + err.Error()
	}
	_ = n.note(line)
}

// storeCalibration records that the user accepted this machine's silence
// levels, so the calibration notice stops.
func storeCalibration() error {
	return updateCaptureConfig(func(c *CaptureConfig) { c.Calibrated = true })
}

// updateCaptureConfig rewrites audioConfig.json with fn applied to its capture
// section, keeping every other field.
func updateCaptureConfig(fn func(*CaptureConfig)) error {
	if utils.ReadonlyConfig {
		return errors.New("audioConfig.json is read-only in this run")
	}
	configDir, err := utils.GetClaiConfigDir()
	if err != nil {
		return diag.Errorf("find config dir: %w", err)
	}
	path := filepath.Join(configDir, "audioConfig.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		return diag.Errorf("read audioConfig.json: %w", err)
	}
	var file Configurations
	if err := json.Unmarshal(raw, &file); err != nil {
		return diag.Errorf("parse audioConfig.json: %w", err)
	}
	fn(&file.Capture)
	if err := utils.CreateFile(path, &file); err != nil {
		return diag.Errorf("write audioConfig.json: %w", err)
	}
	return nil
}
