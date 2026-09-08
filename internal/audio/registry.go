package audio

import (
	"context"
	"fmt"
	"sync"
)

const maxReplacements = 2

// Speaker is one recording-local identity: an immutable ID and one active
// sample cut from verified speech.
type Speaker struct {
	ID            string
	Sample        SampleClip
	SampleVersion int
	Replacements  int
	Verified      int
	Ranges        []SourceInterval
	Frozen        bool
	FrozenReason  string
}

// Snapshot is an immutable copy of the registry at Version.
type Snapshot struct {
	Version  int
	Speakers []Speaker
}

// Samples returns the prefix samples in registry order.
func (s Snapshot) Samples() []SampleClip {
	out := make([]SampleClip, 0, len(s.Speakers))
	for _, sp := range s.Speakers {
		c := sp.Sample
		c.ID = sp.ID
		out = append(out, c)
	}
	return out
}

func (s Snapshot) find(id string) (Speaker, bool) {
	for _, sp := range s.Speakers {
		if sp.ID == id {
			return sp, true
		}
	}
	return Speaker{}, false
}

// SpeakerRegistry is append-only; every identity or sample change goes
// through Discover, which serializes discovery across workers.
type SpeakerRegistry struct {
	discoveryMu sync.Mutex
	mu          sync.RWMutex
	max         int
	version     int
	speakers    []Speaker
}

func NewSpeakerRegistry(maxSpeakers int) *SpeakerRegistry {
	return &SpeakerRegistry{max: maxSpeakers}
}

func (r *SpeakerRegistry) Snapshot() Snapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.snapshotLocked()
}

func (r *SpeakerRegistry) snapshotLocked() Snapshot {
	snap := Snapshot{Version: r.version, Speakers: make([]Speaker, len(r.speakers))}
	for i, sp := range r.speakers {
		sp.Ranges = append([]SourceInterval(nil), sp.Ranges...)
		snap.Speakers[i] = sp
	}
	return snap
}

// RecordVerification is bookkeeping for an accepted sample: it appends the
// source ranges the speaker was mapped to and counts the verification. It
// does not change identity or sample state and does not advance Version.
func (r *SpeakerRegistry) RecordVerification(id string, ranges []SourceInterval) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.speakers {
		if r.speakers[i].ID == id {
			r.speakers[i].Verified++
			r.speakers[i].Ranges = append(r.speakers[i].Ranges, ranges...)
			return
		}
	}
}

// RegistryTx is the single mutation entry point, valid only inside Discover.
type RegistryTx struct {
	r *SpeakerRegistry
}

// Discover runs fn while holding the discovery lock. fn may transcribe;
// all other chunks keep running against their frozen snapshots meanwhile.
func (r *SpeakerRegistry) Discover(ctx context.Context, fn func(tx *RegistryTx) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.discoveryMu.Lock()
	defer r.discoveryMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	return fn(&RegistryTx{r: r})
}

// Snapshot re-reads the registry under the discovery lock.
func (tx *RegistryTx) Snapshot() Snapshot { return tx.r.Snapshot() }

// AddSpeaker applies the "add speaker" row. It refuses past max-speakers.
func (tx *RegistryTx) AddSpeaker(sample SampleClip, ranges []SourceInterval) (id, reason string, ok bool) {
	r := tx.r
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.speakers) >= r.max {
		return "", reasonReserveExhausted, false
	}
	r.version++
	id = fmt.Sprintf("speaker-%d", len(r.speakers)+1)
	sample.ID = id
	r.speakers = append(r.speakers, Speaker{
		ID:            id,
		Sample:        sample,
		SampleVersion: r.version,
		Verified:      1,
		Ranges:        append([]SourceInterval(nil), ranges...),
	})
	return id, "", true
}

// ReplaceSample applies the "replace sample" row with its version re-check,
// or the "freeze" row when the replacement limit is reached or no verified
// range exists. A zero replacement clip means no clip could be cut.
func (tx *RegistryTx) ReplaceSample(id string, observedSampleVersion int, replacement SampleClip) (applied bool, reason string) {
	r := tx.r
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.speakers {
		sp := &r.speakers[i]
		if sp.ID != id {
			continue
		}
		switch {
		case sp.Frozen:
			return false, reasonFrozen
		case sp.SampleVersion != observedSampleVersion:
			return false, reasonStale
		case sp.Replacements >= maxReplacements, len(sp.Ranges) == 0, replacement.End <= replacement.Start:
			r.version++
			sp.Frozen = true
			sp.FrozenReason = fmt.Sprintf("replacements %v of %v, verified ranges %v", sp.Replacements, maxReplacements, len(sp.Ranges))
			return false, reasonFrozen
		}
		r.version++
		replacement.ID = id
		sp.Sample = replacement
		sp.SampleVersion = r.version
		sp.Replacements++
		return true, ""
	}
	return false, "unknown speaker " + id
}
