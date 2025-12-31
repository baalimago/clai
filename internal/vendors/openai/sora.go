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

	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/photo"
	"github.com/baalimago/clai/internal/utils"
	"github.com/baalimago/clai/internal/video"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
	"github.com/baalimago/go_away_boilerplate/pkg/misc"
)

type Sora struct {
	Model   string       `json:"model"`
	Size    string       `json:"size"`
	Seconds string       `json:"seconds"`
	Quality string       `json:"quality"`
	Output  video.Output `json:"output"`

	Prompt         string `json:"-"`
	client         *http.Client
	debug          bool
	apiKey         string
	promptImageB64 string
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

func NewVideoQuerier(vConf video.Configurations) (models.Querier, error) {
	claiConfDir, err := utils.GetClaiConfigDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get config dir: %v", err)
	}
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		return nil, fmt.Errorf("environment variable 'OPENAI_API_KEY' not set")
	}
	model := vConf.Model
	defaultCpy := defaultSora
	defaultCpy.Model = model
	defaultCpy.Output = vConf.Output

	soraQuerier, err := utils.LoadConfigFromFile(claiConfDir, fmt.Sprintf("openai_sora_%v.json", model), nil, &defaultCpy)
	if err != nil {
		ancli.PrintWarn(fmt.Sprintf("failed to load config for model: %v, error: %v\n", model, err))
	}

	if misc.Truthy(os.Getenv("DEBUG")) {
		soraQuerier.debug = true
	}

	soraQuerier.client = &http.Client{}
	soraQuerier.apiKey = apiKey
	soraQuerier.Prompt = vConf.Prompt
	soraQuerier.promptImageB64 = vConf.PromptImageB64

	if soraQuerier.Output.Type == video.UNSET {
		soraQuerier.Output.Type = video.LOCAL
	}

	return &soraQuerier, nil
}

func (q *Sora) createRequest(ctx context.Context) (*http.Request, error) {
	if q.debug {
		tmp := *q
		tmp.apiKey = q.apiKey[:5] + "..."
		ancli.PrintOK(fmt.Sprintf("Sora request: %+v\n", tmp))
	}

	// The working curl uses multipart/form-data directly to /v1/videos.
	// We replicate that here.
	var body bytes.Buffer
	w := multipart.NewWriter(&body)

	if err := w.WriteField("prompt", q.Prompt); err != nil {
		return nil, fmt.Errorf("failed to write prompt field: %w", err)
	}
	if err := w.WriteField("model", q.Model); err != nil {
		return nil, fmt.Errorf("failed to write model field: %w", err)
	}
	if q.Size != "" {
		if err := w.WriteField("size", q.Size); err != nil {
			return nil, fmt.Errorf("failed to write size field: %w", err)
		}
	}
	if q.Seconds != "" {
		if err := w.WriteField("seconds", q.Seconds); err != nil {
			return nil, fmt.Errorf("failed to write seconds field: %w", err)
		}
	}

	// Optional input reference file, like:
	// -F input_reference=@sample_720p.jpeg;type=image/jpeg
	if q.promptImageB64 != "" {
		// NOTE: in our app q.promptImageB64 is base64, not a file path.
		// We save it to disk first (assume png), then attach that file.
		imgPath, err := photo.SaveImage(photo.Output{Dir: os.TempDir(), Prefix: q.Output.Prefix}, q.promptImageB64, "png")
		if err != nil {
			return nil, err
		}
		// best-effort cleanup
		defer func() { _ = os.Remove(imgPath) }()

		f, err := os.Open(imgPath)
		if err != nil {
			return nil, fmt.Errorf("failed to open input_reference file '%s': %w", imgPath, err)
		}
		defer f.Close()

		filename := filepath.Base(imgPath)
		// Set the per-part Content-Type like curl ";type=image/png".
		h := make(textproto.MIMEHeader)
		h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="%s"; filename="%s"`, "input_reference", filename))
		h.Set("Content-Type", "image/png")
		part, err := w.CreatePart(h)
		if err != nil {
			return nil, fmt.Errorf("failed to create input_reference multipart part: %w", err)
		}
		if _, err := io.Copy(part, f); err != nil {
			return nil, fmt.Errorf("failed to copy input_reference into multipart: %w", err)
		}
	}

	if err := w.Close(); err != nil {
		return nil, fmt.Errorf("failed to close multipart writer: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", VideoURL, &body)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", q.apiKey))
	req.Header.Set("Content-Type", w.FormDataContentType())

	return req, nil
}

func (q *Sora) poll(ctx context.Context, id string) error {
	ticker := time.NewTicker(2 * time.Second)
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
				fmt.Println()
				ancli.PrintOK("Video generation completed.\n")
				return nil
			}

			if job.Status == "failed" || job.Status == "cancelled" {
				return fmt.Errorf("video generation %s: %v", job.Status, job.Error)
			}

			fmt.Printf("\rProgress: %d%% (Status: %s)", job.Progress, job.Status)
		}
	}
}

func (q *Sora) download(ctx context.Context, id string) error {
	downloadURL := fmt.Sprintf("%s/%s/content", VideoURL, id)
	if q.Output.Type == video.URL {
		ancli.PrintOK(fmt.Sprintf("Video content URL: %s\n", downloadURL))
		return nil
	}

	req, err := http.NewRequestWithContext(ctx, "GET", downloadURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create download request: %w", err)
	}
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", q.apiKey))

	resp, err := q.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to download video: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to download video, status: %s, body: %s", resp.Status, string(b))
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read video data: %w", err)
	}

	videoName := fmt.Sprintf("%v_%v.mp4", q.Output.Prefix, utils.RandomPrefix())
	outFile := fmt.Sprintf("%v/%v", q.Output.Dir, videoName)
	err = os.WriteFile(outFile, data, 0o644)
	if err != nil {
		ancli.PrintWarn(fmt.Sprintf("failed to write file: '%v', attempting tmp file...\n", err))
		outFile = fmt.Sprintf("/tmp/%v", videoName)
		err = os.WriteFile(outFile, data, 0o644)
		if err != nil {
			return fmt.Errorf("failed to write file: %w", err)
		}
	}
	ancli.PrintOK(fmt.Sprintf("Video saved to: '%v'\n", outFile))

	return nil
}

func (q *Sora) Query(ctx context.Context) error {
	req, err := q.createRequest(ctx)
	if err != nil {
		return err
	}

	resp, err := q.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != 200 {
		return fmt.Errorf("non-OK status: %v, body: %v", resp.Status, string(body))
	}

	var initialJob VideoJob
	if err := json.Unmarshal(body, &initialJob); err != nil {
		return fmt.Errorf("failed to unmarshal response: %w", err)
	}

	ancli.PrintOK(fmt.Sprintf("Video job started. ID: %s\n", initialJob.ID))

	if err := q.poll(ctx, initialJob.ID); err != nil {
		return err
	}

	return q.download(ctx, initialJob.ID)
}
