package audio

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func newAssembler(t *testing.T, runner *scriptedRunner, budgets Budgets) *AudioAssembler {
	t.Helper()
	src := sparseFile(t, 4096)
	a, err := NewAssembler(runner, budgets, src, 5065*time.Second)
	if err != nil {
		t.Fatalf("failed to create assembler: %v", err)
	}
	t.Cleanup(func() { a.Close() })
	return a
}

func twoSampleRequest() AssembleRequest {
	return AssembleRequest{
		Samples: []SampleClip{
			{ID: "spk-1", Start: 436118 * time.Millisecond, End: 444918 * time.Millisecond},
			{ID: "spk-2", Start: 850898 * time.Millisecond, End: 859698 * time.Millisecond},
		},
		Core: SourceInterval{Start: 633 * time.Second, End: 653 * time.Second},
	}
}

func TestAssemblerIsDeterministic(t *testing.T) {
	a := newAssembler(t, &scriptedRunner{fill: 7}, defaultBudgets())
	first, err := a.Assemble(context.Background(), twoSampleRequest())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	second, err := a.Assemble(context.Background(), twoSampleRequest())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if first.RequestHash != second.RequestHash || first.RequestHash == "" {
		t.Errorf("expected identical non-empty request hashes, got %q and %q", first.RequestHash, second.RequestHash)
	}
	if len(first.Regions) != len(second.Regions) {
		t.Fatalf("region count differs: %v vs %v", len(first.Regions), len(second.Regions))
	}
	for i := range first.Regions {
		if first.Regions[i] != second.Regions[i] {
			t.Errorf("region %v differs: %+v vs %+v", i, first.Regions[i], second.Regions[i])
		}
	}
	if first.Duration != second.Duration || first.PrefixDuration != second.PrefixDuration {
		t.Errorf("timing differs: %+v vs %+v", first, second)
	}
	if first.SourceHash == "" || first.SourceHash != second.SourceHash {
		t.Errorf("expected stable source hash, got %q and %q", first.SourceHash, second.SourceHash)
	}
	if first.Codec != requestCodec {
		t.Errorf("expected codec %q, got %q", requestCodec, first.Codec)
	}
}

func TestManifestBoundariesAreSampleExact(t *testing.T) {
	a := newAssembler(t, &scriptedRunner{}, defaultBudgets())
	m, err := a.Assemble(context.Background(), twoSampleRequest())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// prefix: sample, gap, sample, gap; then core
	if len(m.Regions) != 5 {
		t.Fatalf("expected 5 regions, got %+v", m.Regions)
	}
	sample := time.Second / sampleRate
	prev := time.Duration(0)
	for i, r := range m.Regions {
		if r.ReqStart != prev {
			t.Errorf("region %v starts at %v, expected %v", i, r.ReqStart, prev)
		}
		if r.ReqStart%sample != 0 || r.ReqEnd%sample != 0 {
			t.Errorf("region %v boundaries are not on the sample grid: %+v", i, r)
		}
		if r.Kind != RegionGap && r.ReqEnd-r.ReqStart != r.SrcEnd-r.SrcStart {
			t.Errorf("region %v request span %v differs from source span %v", i, r.ReqEnd-r.ReqStart, r.SrcEnd-r.SrcStart)
		}
		prev = r.ReqEnd
	}
	core := m.Regions[4]
	if core.Kind != RegionCore || core.SrcStart != 633*time.Second || core.SrcEnd != 653*time.Second {
		t.Errorf("unexpected core region: %+v", core)
	}
	if m.PrefixDuration != core.ReqStart || m.CoreStart != 633*time.Second {
		t.Errorf("prefix %v / core start %v inconsistent with core region %+v", m.PrefixDuration, m.CoreStart, core)
	}
	// guards: 250 ms each side of the speech interval
	s0 := m.Regions[0]
	if s0.SrcStart != 435868*time.Millisecond || s0.SrcEnd != 445168*time.Millisecond || s0.Label != "spk-1" {
		t.Errorf("unexpected first sample region: %+v", s0)
	}
	if g := m.Regions[1]; g.Kind != RegionGap || g.ReqEnd-g.ReqStart != 500*time.Millisecond {
		t.Errorf("unexpected gap region: %+v", g)
	}
	if m.Duration != prev {
		t.Errorf("manifest duration %v differs from timeline end %v", m.Duration, prev)
	}
	if src, err := m.SourceTime(m.PrefixDuration + 10*time.Second); err != nil || src != 643*time.Second {
		t.Errorf("expected request 10 s into the core to map to 643 s, got %v, %v", src, err)
	}
	if _, err := m.SourceTime(m.Regions[1].ReqStart); err == nil {
		t.Error("expected an error mapping a gap instant to source time")
	}
}

func TestAssemblerRejectsOverBudgetBytes(t *testing.T) {
	budgets := defaultBudgets()
	budgets.MaxRequestBytes = 1 << 20
	runner := &scriptedRunner{encodedBytes: budgets.MaxRequestBytes - 1024}
	a := newAssembler(t, runner, budgets)
	m, err := a.Assemble(context.Background(), twoSampleRequest())
	if err == nil {
		t.Fatalf("expected error, got manifest %+v", m)
	}
	for _, want := range []string{"max-request-bytes 1048576", "16 KiB", "1047552"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("expected error to mention %q, got: %v", want, err)
		}
	}
	assertRunDirEmpty(t, a)
}

func TestAssemblerRejectsOverBudgetSeconds(t *testing.T) {
	budgets := defaultBudgets()
	budgets.MaxRequestDuration = 30 * time.Second
	runner := &scriptedRunner{}
	a := newAssembler(t, runner, budgets)
	req := twoSampleRequest()
	req.Core.End = req.Core.Start + 25*time.Second // 25 s core + ~20 s prefix > 30 s
	_, err := a.Assemble(context.Background(), req)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	for _, want := range []string{"max-request-seconds 30s"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("expected error to mention %q, got: %v", want, err)
		}
	}
	if runner.count("ffmpeg", "libmp3lame") != 0 {
		t.Error("expected no encode after the seconds check failed")
	}
	assertRunDirEmpty(t, a)
}

func TestAssemblerCleansUpOnSuccessAndError(t *testing.T) {
	t.Run("success keeps only the encoded request until Close", func(t *testing.T) {
		a := newAssembler(t, &scriptedRunner{}, defaultBudgets())
		m, err := a.Assemble(context.Background(), twoSampleRequest())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		entries, _ := os.ReadDir(a.RunDir())
		if len(entries) != 1 || filepath.Join(a.RunDir(), entries[0].Name()) != m.FilePath {
			t.Errorf("expected only the encoded file in the run dir, got %v", entries)
		}
		if err := a.Close(); err != nil {
			t.Fatalf("close failed: %v", err)
		}
		if _, err := os.Stat(a.RunDir()); !os.IsNotExist(err) {
			t.Errorf("expected run dir removed, stat err: %v", err)
		}
	})
	t.Run("runner failure mid-stage removes partial artifacts", func(t *testing.T) {
		runner := &scriptedRunner{failOn: "atrim=start=633", failStderr: "Invalid data found"}
		a := newAssembler(t, runner, defaultBudgets())
		_, err := a.Assemble(context.Background(), twoSampleRequest())
		if err == nil || !strings.Contains(err.Error(), "Invalid data found") {
			t.Fatalf("expected error with stderr tail, got %v", err)
		}
		assertRunDirEmpty(t, a)
	})
	t.Run("encode failure removes partial artifacts", func(t *testing.T) {
		runner := &scriptedRunner{failOn: "libmp3lame", failStderr: "Unknown encoder"}
		a := newAssembler(t, runner, defaultBudgets())
		if _, err := a.Assemble(context.Background(), twoSampleRequest()); err == nil || !strings.Contains(err.Error(), "Unknown encoder") {
			t.Fatalf("expected encode error, got %v", err)
		}
		assertRunDirEmpty(t, a)
	})
}

func TestAssemblerCleansUpOnCancel(t *testing.T) {
	runner := &scriptedRunner{blockOn: "atrim=start=633"}
	a := newAssembler(t, runner, defaultBudgets())
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := a.Assemble(ctx, twoSampleRequest())
		done <- err
	}()
	cancel()
	err := <-done
	if err == nil || !strings.Contains(err.Error(), context.Canceled.Error()) {
		t.Fatalf("expected context error, got %v", err)
	}
	assertRunDirEmpty(t, a)
}

func TestAssemblerClipsAtSourceBounds(t *testing.T) {
	a := newAssembler(t, &scriptedRunner{}, defaultBudgets())
	req := AssembleRequest{
		Samples: []SampleClip{
			{ID: "first", Start: 100 * time.Millisecond, End: 4 * time.Second},
			{ID: "last", Start: 5060 * time.Second, End: 5064900 * time.Millisecond},
		},
		Core: SourceInterval{Start: 0, End: 20 * time.Second},
	}
	m, err := a.Assemble(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	first, last := m.Regions[0], m.Regions[2]
	if first.SrcStart != 0 || !first.Clipped {
		t.Errorf("expected first guard clipped at 0 and marked, got %+v", first)
	}
	if last.SrcEnd != 5065*time.Second || !last.Clipped {
		t.Errorf("expected last guard clipped at source end and marked, got %+v", last)
	}
	if m.Regions[4].Clipped {
		t.Errorf("core must not be marked clipped: %+v", m.Regions[4])
	}
}

func assertRunDirEmpty(t *testing.T, a *AudioAssembler) {
	t.Helper()
	entries, err := os.ReadDir(a.RunDir())
	if err != nil {
		t.Fatalf("run dir unreadable: %v", err)
	}
	if len(entries) != 0 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("expected no artifacts left, got %v", names)
	}
}

func TestAssemblerConcurrentRequestsUseDistinctPaths(t *testing.T) {
	a := newAssembler(t, &scriptedRunner{}, defaultBudgets())
	const workers = 4
	manifests := make([]*RequestManifest, workers)
	errs := make([]error, workers)
	var wg sync.WaitGroup
	for i := range workers {
		wg.Go(func() {
			core := SourceInterval{Start: time.Duration(i) * 30 * time.Second, End: time.Duration(i+1) * 30 * time.Second}
			manifests[i], errs[i] = a.Assemble(context.Background(), AssembleRequest{Core: core})
		})
	}
	wg.Wait()
	seen := map[string]int{}
	for i := range workers {
		if errs[i] != nil {
			t.Fatalf("worker %v: %v", i, errs[i])
		}
		seen[manifests[i].FilePath]++
		if _, err := os.Stat(manifests[i].FilePath); err != nil {
			t.Errorf("worker %v: encoded request missing: %v", i, err)
		}
	}
	if len(seen) != workers {
		t.Errorf("expected %v distinct request paths, got %v", workers, seen)
	}
}
