package audio

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
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
		entries, _ := os.ReadDir(a.runDir)
		if len(entries) != 1 || filepath.Join(a.runDir, entries[0].Name()) != m.FilePath {
			t.Errorf("expected only the encoded file in the run dir, got %v", entries)
		}
		if err := a.Close(); err != nil {
			t.Fatalf("close failed: %v", err)
		}
		if _, err := os.Stat(a.runDir); !os.IsNotExist(err) {
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
	entries, err := os.ReadDir(a.runDir)
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

// assemblerProbe is one RequestAssembler under the contract, with what the
// suite can observe of it; runner and runDir are nil and "" for a fake.
type assemblerProbe struct {
	asm    RequestAssembler
	runner *scriptedRunner
	runDir string
}

type assemblerImpl struct {
	name string
	// build applies a seconds budget (0: the default) and, when tight is
	// set, a byte budget the encode exceeds
	build func(t *testing.T, seconds time.Duration, tight bool) assemblerProbe
	// encodes says the assembler encodes, so it has a byte budget
	encodes bool
	// today is the RequestHash and Bytes today's request gets
	todayHash  string
	todayBytes int64
}

func assemblersUnderTest() []assemblerImpl {
	return []assemblerImpl{
		{
			name:    "AudioAssembler",
			encodes: true,
			build: func(t *testing.T, seconds time.Duration, tight bool) assemblerProbe {
				budgets, runner := defaultBudgets(), &scriptedRunner{}
				if seconds > 0 {
					budgets.MaxRequestDuration = seconds
				}
				if tight {
					budgets.MaxRequestBytes = 1 << 20
					runner.encodedBytes = budgets.MaxRequestBytes - 1024
				}
				a := newAssembler(t, runner, budgets)
				return assemblerProbe{asm: a, runner: runner, runDir: a.runDir}
			},
			// captured from Assemble before the zero core existed (Assemble.3)
			todayHash:  "2dfe3f4626eb83a413490f07368acb84163438f1d724f85a3f14cc39c766e995",
			todayBytes: 39936,
		},
		{
			name: "arithmeticAssembler",
			build: func(t *testing.T, seconds time.Duration, _ bool) assemblerProbe {
				return assemblerProbe{asm: &arithmeticAssembler{maxRequestDuration: seconds}}
			},
			todayHash: fmt.Sprintf("%v|%v", twoSampleRequest().Samples, twoSampleRequest().Core),
		},
	}
}

func threeClipRequest() AssembleRequest {
	return AssembleRequest{Samples: []SampleClip{
		{ID: "voice-1", Start: 100 * time.Second, End: 104 * time.Second},
		{ID: "c2-A", Start: 1200 * time.Second, End: 1204 * time.Second, Candidate: true},
		{ID: "c2-B", Start: 1300 * time.Second, End: 1304 * time.Second, Candidate: true},
	}}
}

func TestAssemblerContract(t *testing.T) {
	cases := []struct {
		name    string
		seconds time.Duration
		tight   bool
		req     AssembleRequest
		check   func(t *testing.T, impl assemblerImpl, p assemblerProbe, m *RequestManifest, err error)
	}{
		{
			// Assemble.1, I5
			name: "samples and a zero core hold the clips and gaps only",
			req:  threeClipRequest(),
			check: func(t *testing.T, _ assemblerImpl, p assemblerProbe, m *RequestManifest, err error) {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				want := []RegionKind{RegionSample, RegionGap, RegionSample, RegionGap, RegionSample, RegionGap, RegionCore}
				var kinds []RegionKind
				for _, r := range m.Regions {
					kinds = append(kinds, r.Kind)
				}
				if !slices.Equal(kinds, want) {
					t.Fatalf("regions = %v, want %v", kinds, want)
				}
				core := m.Regions[len(m.Regions)-1]
				if core.ReqStart != m.Duration || core.ReqEnd != m.Duration || core.SrcStart != 0 || core.SrcEnd != 0 {
					t.Errorf("expected a zero-length core region at the end, got %+v", core)
				}
				if m.Duration != m.PrefixDuration || m.CoreStart != 0 || m.CoreEnd != 0 || m.Duration == 0 {
					t.Errorf("expected Duration == PrefixDuration and a zero core, got %+v", m)
				}
				if !m.Regions[2].Candidate || m.Regions[0].Candidate {
					t.Errorf("candidate marks lost: %+v", m.Regions)
				}
				if p.runner != nil && p.runner.count("ffmpeg", "atrim=") != 3 {
					t.Errorf("expected one extraction per clip and none for a core, got %v", p.runner.count("ffmpeg", "atrim="))
				}
			},
		},
		{
			// Assemble.2
			name: "no sample and a zero core is refused",
			req:  AssembleRequest{},
			check: func(t *testing.T, _ assemblerImpl, p assemblerProbe, m *RequestManifest, err error) {
				if !errors.Is(err, ErrEmptyRequest) || m != nil {
					t.Fatalf("expected ErrEmptyRequest, got %+v, %v", m, err)
				}
				if p.runner != nil && len(p.runner.calls) != 0 {
					t.Errorf("expected no ffmpeg call, got %v", p.runner.calls)
				}
				if p.runDir != "" {
					assertRunDirEmpty(t, p.asm.(*AudioAssembler))
				}
			},
		},
		{
			// Assemble.3
			name: "a request with a core is assembled as today",
			req:  twoSampleRequest(),
			check: func(t *testing.T, impl assemblerImpl, _ assemblerProbe, m *RequestManifest, err error) {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				today := manifestFor(twoSampleRequest().Samples, twoSampleRequest().Core)
				if !slices.Equal(m.Regions, today.Regions) || m.PrefixDuration != today.PrefixDuration || m.Duration != today.Duration ||
					m.CoreStart != today.CoreStart || m.CoreEnd != today.CoreEnd {
					t.Errorf("manifest = %+v, want today's %+v", m, today)
				}
				if m.RequestHash != impl.todayHash || m.Bytes != impl.todayBytes {
					t.Errorf("RequestHash %q and Bytes %v, want today's %q and %v", m.RequestHash, m.Bytes, impl.todayHash, impl.todayBytes)
				}
			},
		},
		{
			// Assemble.1 (both budgets), I5
			name:    "a clips-only request over the seconds budget",
			seconds: 10 * time.Second,
			req:     threeClipRequest(),
			check: func(t *testing.T, _ assemblerImpl, p assemblerProbe, m *RequestManifest, err error) {
				if err == nil || !strings.Contains(err.Error(), "exceeds max-request-seconds 10s") {
					t.Fatalf("expected the seconds budget error, got %+v, %v", m, err)
				}
				if p.runner != nil && p.runner.count("ffmpeg", "libmp3lame") != 0 {
					t.Error("expected no encode after the seconds check failed")
				}
				if p.runDir != "" {
					assertRunDirEmpty(t, p.asm.(*AudioAssembler))
				}
			},
		},
		{
			// Assemble.1 (both budgets), I5
			name:  "a clips-only request over the byte budget",
			tight: true,
			req:   threeClipRequest(),
			check: func(t *testing.T, _ assemblerImpl, p assemblerProbe, m *RequestManifest, err error) {
				if err == nil || !strings.Contains(err.Error(), "exceeds max-request-bytes 1048576") {
					t.Fatalf("expected the byte budget error, got %+v, %v", m, err)
				}
				assertRunDirEmpty(t, p.asm.(*AudioAssembler))
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, impl := range assemblersUnderTest() {
				if tc.tight && !impl.encodes {
					continue
				}
				t.Run(impl.name, func(t *testing.T) {
					p := impl.build(t, tc.seconds, tc.tight)
					m, err := p.asm.Assemble(context.Background(), tc.req)
					tc.check(t, impl, p, m, err)
				})
			}
		})
	}
}
