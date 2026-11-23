package generic

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/baalimago/clai/internal/testenv/procenv"
	"github.com/baalimago/clai/pkg/diagnostic"
)

func TestResponseBodyIsTraceOnly(t *testing.T) {
	const body = `{"segments":[{"start":0,"end":1,"speaker":"A","text":"confidential words"}]}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(body))
	}))
	defer server.Close()
	trans := setupTranscriber(t, "gpt-4o-transcribe-diarize", server.URL)
	path := writeTestAudioFile(t, []byte("audio"))
	run := func(level diagnostic.Level) string {
		var out bytes.Buffer
		if _, err := trans.Transcribe(procenv.Diagnostics(context.Background(), &out, level), path); err != nil {
			t.Fatalf("Transcribe: %v", err)
		}
		return out.String()
	}
	debug := run(diagnostic.LevelDebug)
	for _, want := range []string{fmt.Sprintf(`transcription response status="200 OK" bytes=%d`, len(body)), "transcription parsed segments=1"} {
		if !strings.Contains(debug, want) {
			t.Errorf("debug output lacks %q:\n%s", want, debug)
		}
	}
	for _, secret := range []string{"confidential", "test-key"} {
		if strings.Contains(debug, secret) {
			t.Errorf("debug output carries %q:\n%s", secret, debug)
		}
	}
	if trace := run(diagnostic.LevelTrace); !strings.Contains(trace, "transcription response body="+body) {
		t.Errorf("trace output lacks the body:\n%s", trace)
	}
}
