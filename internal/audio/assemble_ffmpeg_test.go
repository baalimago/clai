//go:build ffmpeg

package audio

import (
	"context"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestAssemblerRealFFmpeg runs the assembler against the real binaries on a
// synthesized fixture and checks that the manifest matches the scripted
// runner's within one PCM sample and that the encoded file has the
// timeline's duration. Run: go test -tags ffmpeg -run RealFFmpeg ./internal/audio/
func TestAssemblerRealFFmpeg(t *testing.T) {
	for _, bin := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Fatalf("%v required for this tagged test: %v", bin, err)
		}
	}
	fixture := filepath.Join(t.TempDir(), "fixture.wav")
	gen := exec.Command("ffmpeg", "-v", "error", "-nostdin", "-f", "lavfi", "-i", "sine=frequency=440:duration=4",
		"-ac", "1", "-ar", strconv.Itoa(sampleRate), "-c:a", "pcm_s16le", "-y", fixture)
	if out, err := gen.CombinedOutput(); err != nil {
		t.Fatalf("failed to synthesize fixture: %v: %s", err, out)
	}
	req := AssembleRequest{
		Samples: []SampleClip{{ID: "a", Start: 250 * time.Millisecond, End: 750 * time.Millisecond}},
		Core:    SourceInterval{Start: 1 * time.Second, End: 3 * time.Second},
	}
	budgets := defaultBudgets()
	real, err := NewAssembler(ExecRunner{}, budgets, fixture, 4*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer real.Close()
	scripted, err := NewAssembler(&scriptedRunner{}, budgets, fixture, 4*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer scripted.Close()
	got, err := real.Assemble(context.Background(), req)
	if err != nil {
		t.Fatalf("real assemble failed: %v", err)
	}
	want, err := scripted.Assemble(context.Background(), req)
	if err != nil {
		t.Fatalf("scripted assemble failed: %v", err)
	}
	if len(got.Regions) != len(want.Regions) {
		t.Fatalf("region count differs: %+v vs %+v", got.Regions, want.Regions)
	}
	for i := range got.Regions {
		g, w := got.Regions[i], want.Regions[i]
		if (g.ReqStart-w.ReqStart).Abs() > sampleDuration || (g.ReqEnd-w.ReqEnd).Abs() > sampleDuration || g.SrcStart != w.SrcStart || g.SrcEnd != w.SrcEnd {
			t.Errorf("region %v differs beyond one sample: real %+v scripted %+v", i, g, w)
		}
	}
	if (got.Duration - want.Duration).Abs() > sampleDuration {
		t.Errorf("duration differs: real %v scripted %v", got.Duration, want.Duration)
	}
	probe := exec.Command("ffprobe", "-v", "error", "-show_entries", "format=duration", "-of", "default=nw=1:nk=1", got.FilePath)
	out, err := probe.Output()
	if err != nil {
		t.Fatalf("ffprobe failed: %v", err)
	}
	encoded, _ := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	if diff := encoded - got.Duration.Seconds(); diff < 0 || diff > 0.15 {
		t.Errorf("encoded duration %.3f s vs timeline %.3f s: encoder padding out of range", encoded, got.Duration.Seconds())
	}
	if got.Bytes == 0 || got.Bytes > 64*1024 {
		t.Errorf("unexpected encoded size %v for a 3 s request", got.Bytes)
	}
}
