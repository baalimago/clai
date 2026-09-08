package audio

import (
	"context"
	"strings"
	"testing"
	"time"
)

func clip(id string, start float64) SampleClip {
	return SampleClip{ID: id, Start: time.Duration(start * float64(time.Second)), End: time.Duration((start + 5) * float64(time.Second))}
}

func TestRegistryVersionIncrementsPerMutation(t *testing.T) {
	r := NewSpeakerRegistry(8)
	var ids []string
	err := r.Discover(context.Background(), func(tx *RegistryTx) error {
		for _, start := range []float64{10, 20, 30} {
			id, _, ok := tx.AddSpeaker(clip("", start), []SourceInterval{{Start: 0, End: time.Second}})
			if !ok {
				t.Fatalf("expected add to succeed")
			}
			ids = append(ids, id)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	snap := r.Snapshot()
	if snap.Version != 3 || len(snap.Speakers) != 3 {
		t.Fatalf("expected version 3 with 3 speakers, got %+v", snap)
	}
	if ids[0] == ids[1] || !strings.HasPrefix(ids[0], "speaker-") {
		t.Errorf("unexpected ids %v", ids)
	}
	// Replace one sample: +1
	if err := r.Discover(context.Background(), func(tx *RegistryTx) error {
		applied, _ := tx.ReplaceSample(ids[0], snap.Speakers[0].SampleVersion, clip("", 40))
		if !applied {
			t.Fatal("expected replacement applied")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if v := r.Snapshot().Version; v != 4 {
		t.Errorf("expected version 4 after one replacement, got %v", v)
	}
	// Stale replacement (old sample version) is skipped and does not bump
	if err := r.Discover(context.Background(), func(tx *RegistryTx) error {
		applied, reason := tx.ReplaceSample(ids[0], snap.Speakers[0].SampleVersion, clip("", 50))
		if applied || reason == "" {
			t.Errorf("expected stale replacement skipped with a reason, got applied=%v reason=%q", applied, reason)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if v := r.Snapshot().Version; v != 4 {
		t.Errorf("expected version unchanged at 4, got %v", v)
	}
	// Verification bookkeeping does not bump the identity version
	r.RecordVerification(ids[1], []SourceInterval{{Start: 100 * time.Second, End: 110 * time.Second}})
	after := r.Snapshot()
	// promotion counts as the first verification, the recorded one is the second
	if after.Version != 4 || after.Speakers[1].Verified != 2 || len(after.Speakers[1].Ranges) != 2 {
		t.Errorf("expected verification recorded without version bump, got %+v", after.Speakers[1])
	}
}

func TestRegistrySnapshotIsImmutable(t *testing.T) {
	r := NewSpeakerRegistry(8)
	_ = r.Discover(context.Background(), func(tx *RegistryTx) error {
		tx.AddSpeaker(clip("", 10), nil)
		return nil
	})
	before := r.Snapshot()
	before.Speakers[0].Sample.Start = 999 * time.Second
	before.Speakers[0].Ranges = append(before.Speakers[0].Ranges, SourceInterval{})
	_ = r.Discover(context.Background(), func(tx *RegistryTx) error {
		tx.AddSpeaker(clip("", 20), nil)
		return nil
	})
	if len(before.Speakers) != 1 {
		t.Errorf("old snapshot grew: %+v", before)
	}
	fresh := r.Snapshot()
	if fresh.Speakers[0].Sample.Start != 10*time.Second || len(fresh.Speakers[0].Ranges) != 0 {
		t.Errorf("mutating a snapshot leaked into the registry: %+v", fresh.Speakers[0])
	}
	samples := fresh.Samples()
	if len(samples) != 2 || samples[0].ID != fresh.Speakers[0].ID {
		t.Errorf("expected prefix samples in registry order, got %+v", samples)
	}
}

func TestRegistryReplacementLimit(t *testing.T) {
	r := NewSpeakerRegistry(8)
	var id string
	_ = r.Discover(context.Background(), func(tx *RegistryTx) error {
		id, _, _ = tx.AddSpeaker(clip("", 10), []SourceInterval{{Start: 0, End: 30 * time.Second}})
		return nil
	})
	for i := range 2 {
		snap := r.Snapshot()
		if err := r.Discover(context.Background(), func(tx *RegistryTx) error {
			if applied, reason := tx.ReplaceSample(id, snap.Speakers[0].SampleVersion, clip("", float64(20+i*10))); !applied {
				t.Fatalf("replacement %v should apply, got %q", i, reason)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	snap := r.Snapshot()
	if snap.Speakers[0].Replacements != 2 {
		t.Fatalf("expected 2 replacements, got %+v", snap.Speakers[0])
	}
	if err := r.Discover(context.Background(), func(tx *RegistryTx) error {
		applied, reason := tx.ReplaceSample(id, snap.Speakers[0].SampleVersion, clip("", 60))
		if applied || reason != reasonFrozen {
			t.Errorf("expected freeze instead of a third replacement, got applied=%v reason=%q", applied, reason)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	final := r.Snapshot().Speakers[0]
	if !final.Frozen || final.Sample.Start != 30*time.Second {
		t.Errorf("expected frozen speaker keeping the last good sample, got %+v", final)
	}
	// No verified range: freeze row applies immediately
	r2 := NewSpeakerRegistry(8)
	var id2 string
	_ = r2.Discover(context.Background(), func(tx *RegistryTx) error {
		id2, _, _ = tx.AddSpeaker(clip("", 10), nil)
		return nil
	})
	snap2 := r2.Snapshot()
	_ = r2.Discover(context.Background(), func(tx *RegistryTx) error {
		if applied, reason := tx.ReplaceSample(id2, snap2.Speakers[0].SampleVersion, SampleClip{}); applied || reason != reasonFrozen {
			t.Errorf("expected freeze without verified ranges, got applied=%v reason=%q", applied, reason)
		}
		return nil
	})
	if !r2.Snapshot().Speakers[0].Frozen {
		t.Error("expected speaker frozen")
	}
}

func TestRegistryRefusesBeyondMaxSpeakers(t *testing.T) {
	r := NewSpeakerRegistry(2)
	err := r.Discover(context.Background(), func(tx *RegistryTx) error {
		for i := range 3 {
			id, reason, ok := tx.AddSpeaker(clip("", float64(10*i)), nil)
			if i < 2 && (!ok || id == "") {
				t.Errorf("expected add %v to succeed, got %q", i, reason)
			}
			if i == 2 && (ok || reason != reasonReserveExhausted) {
				t.Errorf("expected third add refused with %q, got ok=%v reason=%q", reasonReserveExhausted, ok, reason)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if n := len(r.Snapshot().Speakers); n != 2 {
		t.Errorf("expected registry unchanged at 2 speakers, got %v", n)
	}
	if r.Snapshot().Version != 2 {
		t.Errorf("expected version 2, got %v", r.Snapshot().Version)
	}
}

func TestRegistryDiscoverHonoursContext(t *testing.T) {
	r := NewSpeakerRegistry(8)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := r.Discover(ctx, func(tx *RegistryTx) error { t.Fatal("callback must not run"); return nil })
	if err == nil {
		t.Fatal("expected context error")
	}
}
