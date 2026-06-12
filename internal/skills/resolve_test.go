package skills

import "testing"

func TestResolveCandidatesTracksShadowedLoser(t *testing.T) {
	a := Skill{Name: "review", SourceClass: "project"}
	b := Skill{Name: "review", SourceClass: "global"}
	res := resolveCandidates([]Candidate{{Skill: a}, {Skill: b}}, nil, precedenceMap{})
	if res.Active["review"].SourceClass != "project" {
		t.Fatalf("wrong winner: %#v", res.Active["review"])
	}
	if len(res.Shadowed) != 1 || res.Shadowed[0].Loser.SourceClass != "global" {
		t.Fatalf("wrong shadowed: %#v", res.Shadowed)
	}
}

func TestResolveCandidates_GlobalRootOrderWins(t *testing.T) {
	earlier := Skill{Name: "review", SourceClass: "global", SourceRoot: "/a"}
	later := Skill{Name: "review", SourceClass: "global", SourceRoot: "/b"}
	res := resolveCandidates(
		[]Candidate{{Skill: later}, {Skill: earlier}},
		nil,
		precedenceMap{"/a": 0, "/b": 1},
	)
	if got := res.Active["review"].SourceRoot; got != "/a" {
		t.Fatalf("expected earlier global root to win, got %q", got)
	}
}
