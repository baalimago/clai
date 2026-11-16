package openrouter

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/audio/generic"
)

type diarizeRequest struct {
	model          string
	responseFormat string
	diarize        []string
	headers        http.Header
}

func recordingServer(t *testing.T, got *diarizeRequest, status int, body []byte) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("failed to parse multipart form: %v", err)
		}
		got.model = r.FormValue("model")
		got.responseFormat = r.FormValue("response_format")
		got.diarize = r.MultipartForm.Value["diarize"]
		got.headers = r.Header.Clone()
		w.WriteHeader(status)
		w.Write(body)
	}))
	t.Cleanup(server.Close)
	return server
}

func setupAt(t *testing.T, model, url string) *Transcriber {
	t.Helper()
	t.Setenv("OPENROUTER_API_KEY", "or-test-key")
	trans := Transcriber{Model: model}
	if err := trans.Setup(); err != nil {
		t.Fatalf("Setup(%q): %v", model, err)
	}
	trans.Transcriber.URL = url
	return &trans
}

func audioFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "meeting.mp3")
	if err := os.WriteFile(path, []byte("fake-mp3"), 0o644); err != nil {
		t.Fatalf("failed to write audio file: %v", err)
	}
	return path
}

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("failed to read fixture %v: %v", name, err)
	}
	return b
}

func TestTranscriber_Diarize_RequestsSpeakerLabels(t *testing.T) {
	var got diarizeRequest
	server := recordingServer(t, &got, http.StatusOK, fixture(t, "scribe_v2_diarized.json"))
	trans := setupAt(t, "or:elevenlabs/scribe-v2+diarize", server.URL)

	segs, err := trans.Transcribe(context.Background(), audioFile(t))
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	if got.model != "elevenlabs/scribe-v2" {
		t.Errorf("model = %q, want the slug without or: and +diarize", got.model)
	}
	if got.responseFormat != "verbose_json" {
		t.Errorf("response_format = %q, want verbose_json", got.responseFormat)
	}
	if len(got.diarize) != 1 || got.diarize[0] != "true" {
		t.Errorf("diarize = %q, want [true]", got.diarize)
	}
	if d := trans.Diarization(); d != generic.DiarizationRequestLocal {
		t.Errorf("Diarization() = %v, want request-local", d)
	}
	want := []generic.Segment{
		{Start: 80 * time.Millisecond, End: 7 * time.Second, Speaker: "A", Text: "Hi everyone, I am Alex. Let's start the weekly planning meeting. First item is the browser demo."},
		{Start: 7340 * time.Millisecond, Speaker: "B", Text: "Thanks, Alex. The demo works, but people have to paste an OpenAI key, which is annoying."},
		{Speaker: "A", Text: "Right. Could we let them sign in with Open Router instead?"},
		{Speaker: "B", Text: "Yes. I will look into the OAuth flow this week"},
	}
	if len(segs) != len(want) {
		t.Fatalf("segments = %+v, want %v speaker turns", segs, len(want))
	}
	for i, w := range want {
		if segs[i].Speaker != w.Speaker || segs[i].Text != w.Text {
			t.Errorf("turn %v = %q %q, want %q %q", i, segs[i].Speaker, segs[i].Text, w.Speaker, w.Text)
		}
		if i > 0 && segs[i].Start < segs[i-1].End {
			t.Errorf("turn %v starts at %v before turn %v ends at %v", i, segs[i].Start, i-1, segs[i-1].End)
		}
	}
	if segs[0].Start != want[0].Start || segs[0].End != want[0].End || segs[1].Start != want[1].Start {
		t.Errorf("turn bounds = %v-%v, %v, want the first and last word of each turn", segs[0].Start, segs[0].End, segs[1].Start)
	}
}

func TestTranscriber_Diarize_KeepsOpenRouterVariant(t *testing.T) {
	var got diarizeRequest
	server := recordingServer(t, &got, http.StatusOK, fixture(t, "scribe_v2_diarized.json"))
	trans := setupAt(t, "or:elevenlabs/scribe-v2:batch+diarize", server.URL)

	if _, err := trans.Transcribe(context.Background(), audioFile(t)); err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	if got.model != "elevenlabs/scribe-v2:batch" {
		t.Errorf("model = %q, want the variant kept and +diarize dropped", got.model)
	}
}

func TestTranscriber_Plain_AsksNoSpeakerLabels(t *testing.T) {
	var got diarizeRequest
	server := recordingServer(t, &got, http.StatusOK, []byte(verboseFixture))
	trans := setupAt(t, "or:openai/whisper-diarize-lookalike", server.URL)

	if _, err := trans.Transcribe(context.Background(), audioFile(t)); err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	if got.responseFormat != "verbose_json" {
		t.Errorf("response_format = %q, want verbose_json: OpenRouter accepts json and verbose_json only", got.responseFormat)
	}
	if len(got.diarize) != 0 {
		t.Errorf("diarize = %q, want no field without +diarize", got.diarize)
	}
	if d := trans.Diarization(); d != generic.DiarizationNone {
		t.Errorf("Diarization() = %v, want none", d)
	}
}

func TestTranscriber_Diarize_UnsupportedModelNamesTheVendorReason(t *testing.T) {
	var got diarizeRequest
	server := recordingServer(t, &got, http.StatusBadRequest, fixture(t, "diarize_unsupported_400.json"))
	trans := setupAt(t, "or:deepgram/nova-3+diarize", server.URL)

	_, err := trans.Transcribe(context.Background(), audioFile(t))
	if err == nil {
		t.Fatal("Transcribe: no error, want the vendor's 400")
	}
	for _, want := range []string{"400", "does not support diarize"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %v, want it to contain %q", err, want)
		}
	}
}

func TestTranscriber_Setup_RefusesAMisplacedSuffix(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "or-test-key")
	for _, model := range []string{"or:elevenlabs/scribe-v2+diarize:batch", "or:elevenlabs/scribe-v2+Diarize", "or:elevenlabs/scribe-v2+diarize+diarize"} {
		trans := Transcriber{Model: model}
		err := trans.Setup()
		if err == nil || !strings.Contains(err.Error(), "must end the name") {
			t.Errorf("Setup(%q) = %v, want a refusal naming where the suffix goes", model, err)
		}
	}
}

func TestTranscriber_Setup_RefusesAnEmptySlug(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "or-test-key")
	for _, model := range []string{"or:", "or:+diarize", "or:  +diarize"} {
		trans := Transcriber{Model: model}
		err := trans.Setup()
		if err == nil || !strings.Contains(err.Error(), "names no model") {
			t.Errorf("Setup(%q) = %v, want an error naming the missing model", model, err)
		}
	}
}

func TestParseDiarized(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want []generic.Segment
	}{
		{
			name: "segment labels when words carry none",
			body: `{"text":"a b","segments":[{"id":0,"start":0,"end":1,"text":" a ","speaker":3,"speaker_label":"speaker_3"},{"id":1,"start":1,"end":2,"text":"b","speaker":1}],"words":[{"word":"a","start":0,"end":1}]}`,
			want: []generic.Segment{{End: time.Second, Speaker: "A", Text: "a"}, {Start: time.Second, End: 2 * time.Second, Speaker: "B", Text: "b"}},
		},
		{
			name: "word labels win over a coarser labeled segment",
			body: `{"text":"hi there yes","segments":[{"id":0,"start":0,"end":3,"text":"hi there yes","speaker":0}],"words":[{"word":"hi","start":0,"end":1,"speaker":0},{"word":"there","start":1,"end":2,"speaker":0},{"word":"yes","start":2,"end":3,"speaker":1}]}`,
			want: []generic.Segment{{End: 2 * time.Second, Speaker: "A", Text: "hi there"}, {Start: 2 * time.Second, End: 3 * time.Second, Speaker: "B", Text: "yes"}},
		},
		{
			name: "integer speakers without labels",
			body: `{"text":"x y z","segments":[{"id":0,"start":0,"end":3,"text":"x y z"}],"words":[{"word":"x","start":0,"end":1,"speaker":1},{"word":"y","start":1,"end":2,"speaker":0},{"word":"z","start":2,"end":3,"speaker":1}]}`,
			want: []generic.Segment{{End: time.Second, Speaker: "A", Text: "x"}, {Start: time.Second, End: 2 * time.Second, Speaker: "B", Text: "y"}, {Start: 2 * time.Second, End: 3 * time.Second, Speaker: "A", Text: "z"}},
		},
		{
			name: "an unlabeled audio event stays in the current turn",
			body: `{"text":"hi (laughs) yes","words":[{"word":"hi","start":0,"end":1,"speaker_label":"speaker_0"},{"word":"(laughs)","start":1,"end":2,"type":"audio_event"},{"word":"yes","start":2,"end":3,"speaker_label":"speaker_0"}]}`,
			want: []generic.Segment{{End: 3 * time.Second, Speaker: "A", Text: "hi (laughs) yes"}},
		},
		{
			name: "silence is no segment",
			body: `{"text":"","segments":[],"words":[]}`,
			want: []generic.Segment{},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseDiarized([]byte(tc.body))
			if err != nil {
				t.Fatalf("ParseDiarized: %v", err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("segments = %+v, want %+v", got, tc.want)
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Errorf("segment %v = %+v, want %+v", i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestParseDiarized_ManySpeakersGetDistinctLabels(t *testing.T) {
	var words []string
	for i := range 28 {
		words = append(words, `{"word":"w","start":0,"end":1,"speaker":`+strconv.Itoa(i)+`}`)
	}
	got, err := ParseDiarized([]byte(`{"text":"w","words":[` + strings.Join(words, ",") + `]}`))
	if err != nil {
		t.Fatalf("ParseDiarized: %v", err)
	}
	seen := map[string]bool{}
	for _, s := range got {
		if seen[s.Speaker] {
			t.Fatalf("label %q given to two speakers: %+v", s.Speaker, got)
		}
		seen[s.Speaker] = true
	}
	if got[25].Speaker != "Z" || got[26].Speaker != "AA" || got[27].Speaker != "AB" {
		t.Errorf("labels 26-28 = %q %q %q, want Z AA AB", got[25].Speaker, got[26].Speaker, got[27].Speaker)
	}
}

func TestParseDiarized_SpeechWithoutLabelsIsTyped(t *testing.T) {
	_, err := ParseDiarized([]byte(`{"text":"hello","segments":[{"id":0,"start":0,"end":1,"text":"hello"}],"words":[{"word":"hello","start":0,"end":1}]}`))
	if !errors.Is(err, ErrNoSpeakerLabels) {
		t.Fatalf("ParseDiarized = %v, want ErrNoSpeakerLabels", err)
	}
}

func TestParseDiarized_MalformedPayloads(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want string
	}{
		{"not json", `{not json`, "invalid character"},
		{"neither segments nor words", `{"text":"hello"}`, "neither segments nor words"},
		{"a string speaker", `{"text":"a","words":[{"word":"a","start":0,"end":1,"speaker":"x"}]}`, "cannot unmarshal string"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseDiarized([]byte(tc.body))
			if !errors.Is(err, ErrMalformedDiarized) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("ParseDiarized = %v, want ErrMalformedDiarized containing %q", err, tc.want)
			}
		})
	}
}
