package generic

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"
)

func streamServer(t *testing.T, rec *recordedRequest, body string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("failed to parse multipart form: %v", err)
		}
		rec.fields = r.MultipartForm.Value
		rec.responseFormat = r.FormValue("response_format")
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte(body))
	}))
}

func streamingTranscriber(t *testing.T, url string) *Transcriber {
	t.Helper()
	trans := setupTranscriber(t, "gpt-4o-transcribe-diarize", url)
	trans.Stream = true
	return trans
}

func TestTranscribe_StreamReportsEachSegmentAsItArrives(t *testing.T) {
	var rec recordedRequest
	server := streamServer(t, &rec, string(loadFixture(t, "openai_diarized_stream.txt")))
	defer server.Close()
	var heard []time.Duration
	ctx := WithHeard(context.Background(), func(end time.Duration) { heard = append(heard, end) })

	got, err := streamingTranscriber(t, server.URL).Transcribe(ctx, writeTestAudioFile(t, []byte("x")))
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	if !slices.Equal(rec.fields["stream"], []string{"true"}) || rec.responseFormat != "diarized_json" {
		t.Errorf("fields = %v, want stream=true with diarized_json", rec.fields)
	}
	want := []Segment{
		{Start: 0, End: 1050 * time.Millisecond, Speaker: "A", Text: "Good morning everyone,"},
		{Start: 1400 * time.Millisecond, End: 3800 * time.Millisecond, Speaker: "A", Text: "and thanks for joining the weekly planning meeting."},
		{Start: 4000 * time.Millisecond, End: 5650 * time.Millisecond, Speaker: "B", Text: "Let us start with the release."},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("segments = %+v, want %+v", got, want)
	}
	if wantHeard := []time.Duration{1050 * time.Millisecond, 3800 * time.Millisecond, 5650 * time.Millisecond}; !slices.Equal(heard, wantHeard) {
		t.Errorf("heard = %v, want each segment's end in order %v", heard, wantHeard)
	}
}

func TestTranscribe_StreamsOnlyWhenAskedAndOffered(t *testing.T) {
	heard := WithHeard(context.Background(), func(time.Duration) {})
	for _, tc := range []struct {
		name   string
		model  string
		stream bool
		ctx    context.Context
	}{
		{"nobody listens", "gpt-4o-transcribe-diarize", true, context.Background()},
		{"the vendor does not stream", "gpt-4o-transcribe-diarize", false, heard},
		{"no timestamped segments to stream", "whisper-1", true, heard},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var rec recordedRequest
			fixture := "openai_diarized.json"
			if tc.model == "whisper-1" {
				fixture = "openai_whisper1_verbose.json"
			}
			server := multipartRecorder(t, &rec, loadFixture(t, fixture))
			defer server.Close()
			trans := setupTranscriber(t, tc.model, server.URL)
			trans.Stream = tc.stream
			if _, err := trans.Transcribe(tc.ctx, writeTestAudioFile(t, []byte("x"))); err != nil {
				t.Fatalf("Transcribe: %v", err)
			}
			if _, ok := rec.fields["stream"]; ok {
				t.Errorf("fields = %v, want no stream field", rec.fields)
			}
		})
	}
}

func TestTranscribe_StreamFailures(t *testing.T) {
	segment := `data: {"type":"transcript.text.segment","text":" hi","speaker":"A","start":0,"end":1}` + "\n\n"
	for _, tc := range []struct {
		name   string
		body   string
		status int
		want   string
		is     error
	}{
		{"ends before transcript.text.done", segment, http.StatusOK, "ended before", ErrStreamEnded},
		{"ends with [DONE] but no transcript.text.done", segment + "data: [DONE]\n\n", http.StatusOK, "ended before", ErrStreamEnded},
		{"an event that is not JSON", "data: {nope\n\n", http.StatusOK, "stream event", nil},
		{"an error event", `data: {"type":"error","error":{"message":"audio too long"}}` + "\n\n", http.StatusOK, "audio too long", nil},
		{"text but no segments", `data: {"type":"transcript.text.done","text":"hi"}` + "\n\n", http.StatusOK, "no segments", nil},
		{"refused", `{"error":"bad"}`, http.StatusBadRequest, "400", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				w.Write([]byte(tc.body))
			}))
			defer server.Close()
			ctx := WithHeard(context.Background(), func(time.Duration) {})
			_, err := streamingTranscriber(t, server.URL).Transcribe(ctx, writeTestAudioFile(t, []byte("x")))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want one containing %q", err, tc.want)
			}
			if tc.is != nil && !errors.Is(err, tc.is) {
				t.Errorf("err = %v, want errors.Is %v", err, tc.is)
			}
		})
	}
}

func TestTranscribe_SilentStreamIsNoSegments(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`data: {"type":"transcript.text.done","text":""}` + "\n\ndata: [DONE]\n\n"))
	}))
	defer server.Close()
	ctx := WithHeard(context.Background(), func(time.Duration) { t.Error("heard reported for silence") })
	got, err := streamingTranscriber(t, server.URL).Transcribe(ctx, writeTestAudioFile(t, []byte("x")))
	if err != nil || len(got) != 0 {
		t.Fatalf("got %v, %v; want no segments and no error", got, err)
	}
}

func TestReportHeard_WithoutListenerIsANoOp(t *testing.T) {
	ReportHeard(context.Background(), time.Second)
	var got []time.Duration
	ReportHeard(WithHeard(context.Background(), func(d time.Duration) { got = append(got, d) }), 2*time.Second)
	if !slices.Equal(got, []time.Duration{2 * time.Second}) {
		t.Fatalf("got %v, want the one report", got)
	}
}

func TestTranscribe_StreamIsAClientField(t *testing.T) {
	trans := setupTranscriber(t, "whisper-1", "http://127.0.0.1:1")
	trans.Fields = map[string]string{"stream": "true"}
	_, err := trans.Transcribe(context.Background(), writeTestAudioFile(t, []byte("x")))
	if err == nil || !strings.Contains(err.Error(), "stream") {
		t.Fatalf("err = %v, want a refusal naming stream", err)
	}
}
