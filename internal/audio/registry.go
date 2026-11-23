package audio

// Speaker is one recording-local identity: an immutable ID and its sample,
// the best of the clips the merge grouped for it.
type Speaker struct {
	ID     string
	Sample SampleClip
	// Clips are the speaker's clips, best first; Sample is Clips[0].
	Clips []SampleClip
}

// Snapshot is the registry as the merge built it, or as the join left it.
type Snapshot struct {
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
