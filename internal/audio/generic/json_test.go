package generic

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestTranscribe_JSON(t *testing.T) {
	var rec recordedRequest
	server := multipartRecorder(t, &rec, loadFixture(t, "openai_gpt_transcribe_json.json"))
	defer server.Close()
	trans := setupTranscriber(t, "gpt-transcribe", server.URL)
	trans.Format = ResponseFormatJSON

	got, err := trans.Transcribe(context.Background(), writeTestAudioFile(t, []byte("RIFF-fake")))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if rec.responseFormat != "json" {
		t.Errorf("expected response_format json, got: %q", rec.responseFormat)
	}
	if rec.chunkingStrategy != "" {
		t.Errorf("expected no chunking_strategy for json, got: %q", rec.chunkingStrategy)
	}
	want := []Segment{{Start: 0, End: 10 * time.Second, Text: "Hello there, this is a short test of the transcription model. The quick brown fox jumps over the lazy dog. Thank you for listening."}}
	if len(got) != 1 || got[0] != want[0] {
		t.Errorf("expected %+v, got %+v", want, got)
	}
}

func TestTranscribe_FormatOverridesModelRule(t *testing.T) {
	var rec recordedRequest
	server := multipartRecorder(t, &rec, loadFixture(t, "openai_whisper1_verbose.json"))
	defer server.Close()
	// A vendor may force verbose_json for a model name the default rule
	// would read as diarized
	trans := setupTranscriber(t, "custom-diarize", server.URL)
	trans.Format = ResponseFormatVerboseJSON

	if _, err := trans.Transcribe(context.Background(), writeTestAudioFile(t, []byte("RIFF-fake"))); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rec.responseFormat != "verbose_json" {
		t.Errorf("expected response_format verbose_json, got: %q", rec.responseFormat)
	}
}

func TestTranscribe_UnknownFormat(t *testing.T) {
	var rec recordedRequest
	server := multipartRecorder(t, &rec, []byte(`{}`))
	defer server.Close()
	trans := setupTranscriber(t, "whisper-1", server.URL)
	trans.Format = ResponseFormat("srt")

	_, err := trans.Transcribe(context.Background(), writeTestAudioFile(t, []byte("RIFF-fake")))
	if err == nil {
		t.Fatal("expected error for unsupported response format, got nil")
	}
	if !strings.Contains(err.Error(), "srt") {
		t.Errorf("expected error naming the format, got: %v", err)
	}
	if rec.model != "" {
		t.Errorf("expected no request to be sent, recorder saw model %q", rec.model)
	}
}

func TestParseJSON(t *testing.T) {
	t.Run("duration usage sets end", func(t *testing.T) {
		segs, err := ParseJSON(loadFixture(t, "openai_gpt_transcribe_json.json"))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(segs) != 1 {
			t.Fatalf("expected 1 segment, got %v", len(segs))
		}
		if segs[0].Start != 0 || segs[0].End != 10*time.Second || segs[0].Speaker != "" {
			t.Errorf("unexpected segment bounds: %+v", segs[0])
		}
		if !strings.HasPrefix(segs[0].Text, "Hello there, this is a short test") {
			t.Errorf("unexpected text: %q", segs[0].Text)
		}
	})
	t.Run("token usage leaves end unknown", func(t *testing.T) {
		segs, err := ParseJSON(loadFixture(t, "openai_gpt4o_transcribe_json.json"))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(segs) != 1 || segs[0].End != 0 {
			t.Errorf("expected one untimed segment, got %+v", segs)
		}
	})
	t.Run("text is trimmed", func(t *testing.T) {
		segs, err := ParseJSON([]byte(`{"text": "  hi there \n"}`))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(segs) != 1 || segs[0].Text != "hi there" {
			t.Errorf("expected trimmed text, got %+v", segs)
		}
	})
	t.Run("empty text is silence", func(t *testing.T) {
		segs, err := ParseJSON([]byte(`{"text": ""}`))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(segs) != 0 {
			t.Errorf("expected no segments for empty text, got %+v", segs)
		}
	})
	t.Run("missing text field", func(t *testing.T) {
		_, err := ParseJSON([]byte(`{"segments": []}`))
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "text") || !strings.Contains(err.Error(), "json") {
			t.Errorf("expected error naming the json payload and text field, got: %v", err)
		}
	})
	t.Run("malformed", func(t *testing.T) {
		_, err := ParseJSON([]byte(`{not json`))
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "json payload") {
			t.Errorf("expected error naming the json payload, got: %v", err)
		}
	})
	t.Run("negative duration", func(t *testing.T) {
		_, err := ParseJSON([]byte(`{"text": "hi", "usage": {"type": "duration", "seconds": -1}}`))
		if err == nil {
			t.Fatal("expected error for negative duration, got nil")
		}
		if !strings.Contains(err.Error(), "-1") {
			t.Errorf("expected error naming the duration, got: %v", err)
		}
	})
}

func TestRequestFormat(t *testing.T) {
	trans := setupTranscriber(t, "whisper-1", "http://localhost")
	if got := trans.RequestFormat(); got != ResponseFormatVerboseJSON {
		t.Errorf("expected negotiated verbose_json, got %v", got)
	}
	trans.Format = ResponseFormatJSON
	if got := trans.RequestFormat(); got != ResponseFormatJSON {
		t.Errorf("expected explicit json, got %v", got)
	}
}
