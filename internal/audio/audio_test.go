package audio

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func loadFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("failed to read fixture %v: %v", name, err)
	}
	return b
}

func TestParseVerboseJSON_Whisper1Fixture(t *testing.T) {
	got, err := ParseVerboseJSON(loadFixture(t, "openai_whisper1_verbose.json"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []Segment{
		{Start: 0, End: 5280 * time.Millisecond, Text: "Hello and welcome to the meeting."},
		{Start: 5280 * time.Millisecond, End: 9040 * time.Millisecond, Text: "Let's get started with the agenda."},
	}
	assertSegments(t, want, got)
}

func TestParseVerboseJSON_OpenRouterFixture(t *testing.T) {
	got, err := ParseVerboseJSON(loadFixture(t, "openrouter_verbose.json"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []Segment{
		{Start: 0, End: 3840 * time.Millisecond, Text: "Testing OpenRouter transcription."},
	}
	assertSegments(t, want, got)
}

func TestParseDiarizedJSON_PreservesSpeakers(t *testing.T) {
	got, err := ParseDiarizedJSON(loadFixture(t, "openai_diarized.json"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []Segment{
		{Start: 0, End: 4900 * time.Millisecond, Speaker: "A", Text: "Hi Bob, how are you?"},
		{Start: 5100 * time.Millisecond, End: 12500 * time.Millisecond, Speaker: "B", Text: "I'm doing great, thanks Alice."},
	}
	assertSegments(t, want, got)
}

func assertSegments(t *testing.T, want, got []Segment) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("expected %v segments, got %v: %+v", len(want), len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("segment %v: expected %+v, got %+v", i, want[i], got[i])
		}
	}
}

func TestParse_MalformedJSON(t *testing.T) {
	testCases := []struct {
		name      string
		parse     func([]byte) ([]Segment, error)
		wantInErr string
	}{
		{"verbose", ParseVerboseJSON, "verbose_json"},
		{"diarized", ParseDiarizedJSON, "diarized_json"},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.parse([]byte("{not json"))
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantInErr) {
				t.Errorf("expected error naming %q, got: %v", tc.wantInErr, err)
			}
		})
	}
}

func TestParse_MissingSegmentsField(t *testing.T) {
	testCases := []struct {
		name  string
		parse func([]byte) ([]Segment, error)
	}{
		{"verbose", ParseVerboseJSON},
		{"diarized", ParseDiarizedJSON},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.parse([]byte(`{"text": "hello", "duration": 1.0}`))
			if err == nil {
				t.Fatal("expected error for missing segments field, got nil")
			}
			if !strings.Contains(err.Error(), "segments") {
				t.Errorf("expected error naming missing segments field, got: %v", err)
			}
		})
	}
}

func TestParse_EmptySegmentsArray(t *testing.T) {
	testCases := []struct {
		name  string
		parse func([]byte) ([]Segment, error)
	}{
		{"verbose", ParseVerboseJSON},
		{"diarized", ParseDiarizedJSON},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.parse([]byte(`{"segments": []}`))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got == nil {
				t.Fatal("expected non-nil empty slice")
			}
			if len(got) != 0 {
				t.Fatalf("expected empty slice, got: %+v", got)
			}
		})
	}
}

func TestOffset(t *testing.T) {
	segs := []Segment{
		{Start: 0, End: time.Second, Speaker: "A", Text: "one"},
		{Start: time.Second, End: 2 * time.Second, Text: "two"},
	}
	t.Run("shifts start and end uniformly", func(t *testing.T) {
		got := Offset(segs, 10*time.Second)
		want := []Segment{
			{Start: 10 * time.Second, End: 11 * time.Second, Speaker: "A", Text: "one"},
			{Start: 11 * time.Second, End: 12 * time.Second, Text: "two"},
		}
		assertSegments(t, want, got)
	})
	t.Run("no-op at delta 0", func(t *testing.T) {
		got := Offset(segs, 0)
		assertSegments(t, segs, got)
	})
	t.Run("does not mutate input", func(t *testing.T) {
		Offset(segs, time.Hour)
		if segs[0].Start != 0 {
			t.Errorf("input mutated: %+v", segs[0])
		}
	})
}

var diarizedSegs = []Segment{
	{Start: 0, End: 5280 * time.Millisecond, Speaker: "A", Text: "Hello and welcome to the meeting."},
	{Start: 5280 * time.Millisecond, End: 9040 * time.Millisecond, Speaker: "B", Text: "Let's get started with the agenda."},
}

var plainSegs = []Segment{
	{Start: 0, End: 5280 * time.Millisecond, Text: "Hello and welcome to the meeting."},
	{Start: 5280 * time.Millisecond, End: 9040 * time.Millisecond, Text: "Let's get started with the agenda."},
}

func TestRender(t *testing.T) {
	testCases := []struct {
		name   string
		segs   []Segment
		format OutputFormat
		want   string
	}{
		{
			name:   "vtt with speakers",
			segs:   diarizedSegs,
			format: FormatVTT,
			want: `WEBVTT

00:00:00.000 --> 00:00:05.280
<v A>Hello and welcome to the meeting.</v>

00:00:05.280 --> 00:00:09.040
<v B>Let's get started with the agenda.</v>
`,
		},
		{
			name:   "vtt without speakers",
			segs:   plainSegs,
			format: FormatVTT,
			want: `WEBVTT

00:00:00.000 --> 00:00:05.280
Hello and welcome to the meeting.

00:00:05.280 --> 00:00:09.040
Let's get started with the agenda.
`,
		},
		{
			name:   "srt with speakers",
			segs:   diarizedSegs,
			format: FormatSRT,
			want: `1
00:00:00,000 --> 00:00:05,280
A: Hello and welcome to the meeting.

2
00:00:05,280 --> 00:00:09,040
B: Let's get started with the agenda.
`,
		},
		{
			name:   "srt without speakers",
			segs:   plainSegs,
			format: FormatSRT,
			want: `1
00:00:00,000 --> 00:00:05,280
Hello and welcome to the meeting.

2
00:00:05,280 --> 00:00:09,040
Let's get started with the agenda.
`,
		},
		{
			name:   "text with speakers",
			segs:   diarizedSegs,
			format: FormatText,
			want:   "A: Hello and welcome to the meeting.\nB: Let's get started with the agenda.\n",
		},
		{
			name:   "text without speakers",
			segs:   plainSegs,
			format: FormatText,
			want:   "Hello and welcome to the meeting.\nLet's get started with the agenda.\n",
		},
		{
			name:   "json with speakers",
			segs:   diarizedSegs,
			format: FormatJSON,
			want:   `[{"start":0,"end":5.28,"speaker":"A","text":"Hello and welcome to the meeting."},{"start":5.28,"end":9.04,"speaker":"B","text":"Let's get started with the agenda."}]`,
		},
		{
			name:   "json without speakers",
			segs:   plainSegs,
			format: FormatJSON,
			want:   `[{"start":0,"end":5.28,"text":"Hello and welcome to the meeting."},{"start":5.28,"end":9.04,"text":"Let's get started with the agenda."}]`,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Render(tc.segs, tc.format)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("expected:\n%q\ngot:\n%q", tc.want, got)
			}
		})
	}
}

func TestRenderJSON_FloatSecondsContract(t *testing.T) {
	got, err := Render(plainSegs, FormatJSON)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(got, `"end":5.28`) {
		t.Errorf("expected float seconds 5.28 in output, got: %v", got)
	}
	if strings.Contains(got, "speaker") {
		t.Errorf("expected speaker to be omitted, got: %v", got)
	}
	if strings.Contains(got, "5280000000") {
		t.Errorf("nanosecond integer leaked into output: %v", got)
	}
}

func TestRenderEmpty(t *testing.T) {
	testCases := []struct {
		format OutputFormat
		want   string
	}{
		{FormatVTT, "WEBVTT\n"},
		{FormatSRT, ""},
		{FormatText, ""},
		{FormatJSON, "[]"},
	}
	for _, tc := range testCases {
		t.Run(string(tc.format), func(t *testing.T) {
			got, err := Render([]Segment{}, tc.format)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("expected %q, got %q", tc.want, got)
			}
		})
	}
}

func TestRenderGarbageTimestampsDoesNotPanic(t *testing.T) {
	garbage := []Segment{
		{Start: -3 * time.Second, End: -time.Second, Text: "negative"},
		{Start: 10 * time.Second, End: 2 * time.Second, Text: "end before start"},
	}
	for _, format := range []OutputFormat{FormatVTT, FormatSRT, FormatText, FormatJSON} {
		t.Run(string(format), func(t *testing.T) {
			if _, err := Render(garbage, format); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestRenderUnknownFormat(t *testing.T) {
	_, err := Render(plainSegs, OutputFormat("yaml"))
	if err == nil {
		t.Fatal("expected error for unknown format, got nil")
	}
}

func TestParseOutputFormat(t *testing.T) {
	t.Run("valid values", func(t *testing.T) {
		testCases := []struct {
			in   string
			want OutputFormat
		}{
			{"vtt", FormatVTT},
			{"srt", FormatSRT},
			{"text", FormatText},
			{"json", FormatJSON},
		}
		for _, tc := range testCases {
			got, err := ParseOutputFormat(tc.in)
			if err != nil {
				t.Fatalf("unexpected error for %q: %v", tc.in, err)
			}
			if got != tc.want {
				t.Errorf("expected %v, got %v", tc.want, got)
			}
		}
	})
	t.Run("unknown value lists valid formats", func(t *testing.T) {
		_, err := ParseOutputFormat("yaml")
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		for _, valid := range []string{"vtt", "srt", "text", "json"} {
			if !strings.Contains(err.Error(), valid) {
				t.Errorf("expected error to list %q, got: %v", valid, err)
			}
		}
	})
}
