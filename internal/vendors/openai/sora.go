package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"os"
	"path/filepath"
	"time"

	"github.com/baalimago/clai/internal/apikey"
	"github.com/baalimago/clai/internal/diag"
	"github.com/baalimago/clai/internal/display"
	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/runenv"
	"github.com/baalimago/clai/internal/stdio"
	"github.com/baalimago/clai/internal/utils"
	video "github.com/baalimago/clai/internal/video/generic"
)

type Sora struct {
	Model   string       `json:"model"`
	Size    string       `json:"size"`
	Seconds string       `json:"seconds"`
	Quality string       `json:"quality"`
	Output  video.Output `json:"output"`

	Prompt         string `json:"-"`
	client         *http.Client
	apiKey         string
	promptImageB64 string
	// pollEvery overrides soraPollInterval; tests shorten it.
	pollEvery time.Duration
}

const soraPollInterval = 2 * time.Second

func (q *Sora) pollInterval() time.Duration {
	if q.pollEvery > 0 {
		return q.pollEvery
	}
	return soraPollInterval
}

type VideoJob struct {
	ID       string `json:"id"`
	Status   string `json:"status"`
	Progress int    `json:"progress"`
	Error    any    `json:"error"`
}

var defaultSora = Sora{
	Model:   "sora-2",
	Size:    "720x1280",
	Seconds: "4",
}

func NewVideoQuerier(ctx context.Context, vConf video.Configurations) (models.Querier, error) {
	claiConfDir, err := utils.GetClaiConfigDir()
	if err != nil {
		return nil, diag.Errorf("failed to get config dir: %w", err)
	}
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		return nil, &apikey.MissingError{Env: "OPENAI_API_KEY", Message: "environment variable 'OPENAI_API_KEY' not set"}
	}
	model := vConf.Model
	defaultCpy := defaultSora
	defaultCpy.Model = model
	defaultCpy.Output = vConf.Output

	soraQuerier, err := utils.LoadConfigFromFile(ctx, claiConfDir, fmt.Sprintf("openai_sora_%v.json", model), nil, &defaultCpy)
	if err != nil {
		runenv.Warnf(ctx, runenv.SubsystemVendorOpenAI, "failed to load config for model: %v, error: %v", model, err)
	}

	soraQuerier.client = vConf.Client
	if soraQuerier.client == nil {
		soraQuerier.client = &http.Client{}
	}
	soraQuerier.apiKey = apiKey
	soraQuerier.Prompt = vConf.Prompt
	soraQuerier.promptImageB64 = vConf.PromptImageB64

	if soraQuerier.Output.Type == video.UNSET {
		soraQuerier.Output.Type = video.LOCAL
	}

	return &soraQuerier, nil
}

func (q *Sora) createRequest(ctx context.Context) (*http.Request, error) {
	if runenv.DebugEnabled(ctx, runenv.SubsystemVendorOpenAI) {
		tmp := *q
		tmp.apiKey = q.apiKey[:min(5, len(q.apiKey))] + "..."
		runenv.Debugf(ctx, runenv.SubsystemVendorOpenAI, "Sora request: %+v", tmp)
	}

	// The working curl uses multipart/form-data directly to /v1/videos.
	// We replicate that here.
	var body bytes.Buffer
	w := multipart.NewWriter(&body)

	if err := w.WriteField("prompt", q.Prompt); err != nil {
		return nil, diag.Errorf("failed to write prompt field: %w", err)
	}
	if err := w.WriteField("model", q.Model); err != nil {
		return nil, diag.Errorf("failed to write model field: %w", err)
	}
	if q.Size != "" {
		if err := w.WriteField("size", q.Size); err != nil {
			return nil, diag.Errorf("failed to write size field: %w", err)
		}
	}
	if q.Seconds != "" {
		if err := w.WriteField("seconds", q.Seconds); err != nil {
			return nil, diag.Errorf("failed to write seconds field: %w", err)
		}
	}

	// Optional input reference file, like:
	// -F input_reference=@sample_720p.jpeg;type=image/jpeg
	if q.promptImageB64 != "" {
		// NOTE: in our app q.promptImageB64 is base64, not a file path.
		// We save it to disk first (assume png), then attach that file.
		imgPath, err := utils.SaveBase64File(q.Output.Prefix, os.TempDir(), q.promptImageB64, "png")
		if err != nil {
			return nil, err
		}
		// best-effort cleanup
		defer func() { _ = os.Remove(imgPath) }()

		f, err := os.Open(imgPath)
		if err != nil {
			return nil, diag.Errorf("failed to open input_reference file '%s': %w", imgPath, err)
		}
		defer f.Close()

		filename := filepath.Base(imgPath)
		// Set the per-part Content-Type like curl ";type=image/png".
		h := make(textproto.MIMEHeader)
		h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="%s"; filename="%s"`, "input_reference", filename))
		h.Set("Content-Type", "image/png")
		part, err := w.CreatePart(h)
		if err != nil {
			return nil, diag.Errorf("failed to create input_reference multipart part: %w", err)
		}
		if _, err := io.Copy(part, f); err != nil {
			return nil, diag.Errorf("failed to copy input_reference into multipart: %w", err)
		}
	}

	if err := w.Close(); err != nil {
		return nil, diag.Errorf("failed to close multipart writer: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", VideoURL, &body)
	if err != nil {
		return nil, diag.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", q.apiKey))
	req.Header.Set("Content-Type", w.FormDataContentType())

	return req, nil
}

func (q *Sora) poll(ctx context.Context, id string) error {
	progress, err := newSoraProgress(runenv.From(ctx))
	if err != nil {
		return err
	}
	pollErr := q.pollJob(ctx, id, progress)
	if err := progress.end(pollErr); err != nil && pollErr == nil {
		return err
	}
	if pollErr == nil {
		runenv.Noticef(ctx, runenv.SubsystemVideo, "Video generation completed.")
	}
	return pollErr
}

func (q *Sora) pollJob(ctx context.Context, id string, progress *soraProgress) error {
	ticker := time.NewTicker(q.pollInterval())
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			req, err := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("%s/%s", VideoURL, id), nil)
			if err != nil {
				return err
			}
			req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", q.apiKey))

			resp, err := q.client.Do(req)
			if err != nil {
				return err
			}

			body, err := io.ReadAll(resp.Body)
			resp.Body.Close()
			if err != nil {
				return err
			}

			var job VideoJob
			if err := json.Unmarshal(body, &job); err != nil {
				return err
			}

			if job.Status == "completed" {
				return nil
			}

			if job.Status == "failed" || job.Status == "cancelled" {
				// job.Error is any: it may carry an error value, an object or a
				// string. Wrap with %w only when it is an error so the chain stays
				// typed; otherwise preserve the raw content (worklog
				// 2026-09-05-error-propagation, phase 8, B7).
				if err, ok := job.Error.(error); ok {
					return diag.Errorf("video generation %s: %w", job.Status, err)
				}
				return fmt.Errorf("video generation %s: %v", job.Status, job.Error)
			}

			if err := progress.update(fmt.Sprintf("Progress: %d%% (Status: %s)", job.Progress, job.Status)); err != nil {
				return err
			}
		}
	}
}

func (q *Sora) download(ctx context.Context, id string) error {
	downloadURL := fmt.Sprintf("%s/%s/content", VideoURL, id)
	if q.Output.Type == video.URL {
		if _, err := fmt.Fprintf(runenv.From(ctx).Stdout(), "Video content URL: %s\n", downloadURL); err != nil {
			return diag.Errorf("print video content URL: %w", err)
		}
		return nil
	}

	req, err := http.NewRequestWithContext(ctx, "GET", downloadURL, nil)
	if err != nil {
		return diag.Errorf("failed to create download request: %w", err)
	}
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", q.apiKey))

	resp, err := q.client.Do(req)
	if err != nil {
		return diag.Errorf("failed to download video: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to download video, status: %s, body: %s", resp.Status, string(b))
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return diag.Errorf("failed to read video data: %w", err)
	}

	videoName := fmt.Sprintf("%v_%v.mp4", q.Output.Prefix, utils.RandomPrefix())
	outFile := fmt.Sprintf("%v/%v", q.Output.Dir, videoName)
	err = os.WriteFile(outFile, data, 0o644)
	if err != nil {
		runenv.Warnf(ctx, runenv.SubsystemVideo, "failed to write file: '%v', attempting tmp file...", err)
		outFile = filepath.Join(os.TempDir(), videoName)
		err = os.WriteFile(outFile, data, 0o644)
		if err != nil {
			return diag.Errorf("failed to write file: %w", err)
		}
	}
	runenv.Noticef(ctx, runenv.SubsystemVideo, "Video saved to: '%v'", outFile)

	return nil
}

func (q *Sora) Query(ctx context.Context) error {
	req, err := q.createRequest(ctx)
	if err != nil {
		return err
	}

	resp, err := q.client.Do(req)
	if err != nil {
		return diag.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return diag.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != 200 {
		return fmt.Errorf("non-OK status: %v, body: %v", resp.Status, string(body))
	}

	var initialJob VideoJob
	if err := json.Unmarshal(body, &initialJob); err != nil {
		return diag.Errorf("failed to unmarshal response: %w", err)
	}

	runenv.Noticef(ctx, runenv.SubsystemVideo, "Video job started. ID: %s", initialJob.ID)

	if err := q.poll(ctx, initialJob.ID); err != nil {
		return err
	}

	return q.download(ctx, initialJob.ID)
}

// soraProgress shows the job's progress on stderr: one redrawn line on a
// terminal, a line per change in a pipe, a progress record per change under
// -json.
type soraProgress struct {
	out   io.Writer
	live  bool
	disp  display.Displayer
	last  string
	drawn bool
}

const soraProgressTitle = "video"

func newSoraProgress(env runenv.Env) (*soraProgress, error) {
	p := &soraProgress{out: env.Stderr(), live: env.IsTerminal(stdio.Stderr)}
	if env.JSON() {
		disp, err := env.Displayer(stdio.Stderr)
		if err != nil {
			return nil, diag.Errorf("video progress: %w", err)
		}
		p.disp = disp
	}
	return p, nil
}

func (p *soraProgress) update(detail string) error {
	if detail == p.last {
		return nil
	}
	p.last = detail
	var err error
	switch {
	case p.disp != nil:
		err = p.disp.ReportProgress(display.Progress{Title: soraProgressTitle, State: "update", Detail: detail})
	case p.live:
		p.drawn = true
		_, err = fmt.Fprintf(p.out, "\r%s", detail)
	default:
		_, err = fmt.Fprintln(p.out, detail)
	}
	if err != nil {
		return diag.Errorf("show video progress: %w", err)
	}
	return nil
}

// end closes the progress: done or failed under -json, the newline after a
// drawn line on a terminal.
func (p *soraProgress) end(runErr error) error {
	var err error
	switch {
	case p.disp != nil:
		state := display.Progress{Title: soraProgressTitle, State: "done"}
		if runErr != nil {
			state.State, state.Detail = "failed", runErr.Error()
		}
		err = p.disp.ReportProgress(state)
	case p.drawn:
		_, err = fmt.Fprintln(p.out)
	}
	if err != nil {
		return diag.Errorf("end video progress: %w", err)
	}
	return nil
}
