package voices_test

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/audio/voices"
)

// ranked is one clip of a hand-written log. Its id is its letter twelve
// times, and every time is a number of days before the ranking.
type ranked struct {
	id                                 string
	added                              float64
	named, overruled, unlanded, landed []float64
}

func uses(n int, daysAgo float64) []float64 { return slices.Repeat([]float64{daysAgo}, n) }

// rankOrder is the letters of the clips in the order List gives them.
func rankOrder(t *testing.T, clips ...ranked) string {
	t.Helper()
	e := newEnv(t)
	e.step = 0
	var lines []string
	for _, c := range clips {
		log := func(kind, extra string, days ...float64) {
			for _, d := range days {
				at := t0.Add(-time.Duration(d * 24 * float64(time.Hour)))
				lines = append(lines, ev(len(lines), at, kind, voices.ClipID(strings.Repeat(c.id, 12)), extra))
			}
		}
		log("added", confirmed(8), c.added)
		log("named", spoke, c.named...)
		log("overruled", "", c.overruled...)
		log("placed", `,"landed":false`, c.unlanded...)
		log("placed", `,"landed":true`, c.landed...)
	}
	e.write("lorentz", `{"name":"Lorentz"}`, lines...)
	var order string
	for _, id := range ids(e.clips("lorentz")) {
		order += string(id[:1])
	}
	return order
}

func TestRank_Rules(t *testing.T) {
	once := []float64{50}
	for _, tc := range []struct {
		name  string
		clips []ranked
		want  string
	}{
		{"more good uses first; equal clips by the later added, then by id", []ranked{
			{id: "a", added: 10, named: uses(3, 5)},
			{id: "b", added: 9, named: uses(3, 5)},
			{id: "d", added: 7},
			{id: "e", added: 7},
			{id: "f", added: 20, named: uses(10, 3)},
			{id: "1", added: 1, named: uses(3, 0.5), overruled: uses(3, 0.4)},
		}, "fbade1"},
		{"the worklog's menu example: often named, overruled as often as named, never used", []ranked{
			{id: "1", added: 1.8},
			{id: "e", added: 7.2, named: []float64{7.1, 6.1, 5.1}, overruled: []float64{6, 5}},
			{id: "9", added: 8.1, named: uses(14, 4), landed: uses(14, 4), overruled: []float64{6}},
		}, "9e1"},
		{"a placement that did not land counts against", []ranked{
			{id: "0", added: 30, named: uses(5, 10), unlanded: uses(10, 9)}, {id: "1", added: 30, named: uses(5, 10)},
		}, "10"},
		{"also on a clip that never named anyone", []ranked{{id: "0", added: 30, unlanded: uses(3, 9)}, {id: "1", added: 30}}, "10"},
		{"a few uses move a clip only a little", []ranked{
			{id: "a", added: 0.9, named: uses(1, 0.5)},
			{id: "b", added: 0.9, named: uses(20, 0.5), overruled: uses(1, 0.5)},
			{id: "c", added: 0.9},
			{id: "d", added: 0.9, named: uses(5, 0.5), overruled: uses(5, 0.5)},
			{id: "e", added: 0.9, named: uses(3, 0.5), overruled: uses(2, 0.5)},
		}, "baecd"},
		{"what happened lately weighs more", []ranked{
			{id: "0", added: 350, named: uses(4, 300), overruled: uses(2, 3)},
			{id: "1", added: 400, named: slices.Concat(uses(2, 300), uses(2, 3)), overruled: uses(2, 300)},
		}, "10"},
		{"placements that landed count for nothing", []ranked{{id: "1", added: 60, named: once}, {id: "0", added: 60, named: once, landed: uses(30, 20)}}, "01"},
		{"nor against", []ranked{{id: "1", added: 60, named: once, landed: uses(30, 20)}, {id: "0", added: 60, named: once}}, "01"},
	} {
		reversed := slices.Clone(tc.clips)
		slices.Reverse(reversed)
		if got, back := rankOrder(t, tc.clips...), rankOrder(t, reversed...); got != tc.want || back != tc.want {
			t.Errorf("%s: ranked %s, and %s with the log reversed; want %s", tc.name, got, back, tc.want)
		}
	}
}
