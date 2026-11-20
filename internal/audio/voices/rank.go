package voices

import (
	"cmp"
	"math"
	"slices"
	"strings"
	"time"
)

// The ranking's constants are code, not configuration.
const (
	// Every clip starts with one good and three bad pseudo-uses: an untried
	// clip scores a quarter, and a few uses move it only a little.
	rankPriorGood = 1.0
	rankPriorBad  = 3.0
	// A use this old counts half as much as one made now.
	rankHalfLife = 90 * 24 * time.Hour
)

// rank returns the clips best first, as of now. It alone decides which clips
// are picked for naming and which are pruned. Equal scores rank by the later
// added time, then by slug and id.
func rank(clips []clip, now time.Time) []clip {
	type scored struct {
		clip  clip
		score float64
	}
	all := make([]scored, len(clips))
	for i, c := range clips {
		all[i] = scored{clip: c, score: c.score(now)}
	}
	slices.SortFunc(all, func(a, b scored) int {
		return cmp.Or(
			cmp.Compare(b.score, a.score),
			b.clip.added.Compare(a.clip.added),
			strings.Compare(string(a.clip.ref.Slug), string(b.clip.ref.Slug)),
			strings.Compare(string(a.clip.ref.ID), string(b.clip.ref.ID)),
		)
	})
	ranked := make([]clip, len(all))
	for i, s := range all {
		ranked[i] = s.clip
	}
	return ranked
}

// score is the smoothed, recency-weighted share of the clip's uses that went
// well. A use is a naming or a placement that did not land; an overrule
// turns one naming bad.
func (c clip) score(now time.Time) float64 {
	var named, overruled, unlanded float64
	for _, e := range c.events {
		weight := 1.0
		if age := now.Sub(e.At); age > 0 {
			weight = math.Exp2(-float64(age) / float64(rankHalfLife))
		}
		switch {
		case e.Kind == eventNamed:
			named += weight
		case e.Kind == eventOverruled:
			overruled += weight
		case e.Kind == eventPlaced && !*e.Landed:
			unlanded += weight
		}
	}
	good := max(0, named-overruled)
	bad := overruled + unlanded
	return (good + rankPriorGood) / (good + bad + rankPriorGood + rankPriorBad)
}
