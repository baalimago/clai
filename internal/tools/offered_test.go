package tools

import (
	"context"
	"errors"
	"slices"
	"testing"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

type offeredTestTool struct {
	name string
	out  string
	err  error
	runs *int
}

func (o offeredTestTool) Call(pub_models.Input) (string, error) {
	if o.runs != nil {
		*o.runs++
	}
	return o.out, o.err
}

func (o offeredTestTool) Specification() pub_models.Specification {
	return pub_models.Specification{Name: o.name}
}

type offeredContextTool struct {
	offeredTestTool
	got pub_models.Input
}

func (o *offeredContextTool) CallWithContext(_ context.Context, in pub_models.Input) (string, error) {
	o.got = in
	return "with context", nil
}

type offeredTestBox struct{ registered []string }

func (b *offeredTestBox) RegisterTool(tool pub_models.LLMTool) {
	b.registered = append(b.registered, tool.Specification().Name)
}

func call(name string) pub_models.Call { return pub_models.Call{Name: name} }

func TestOffered_onlyAnOfferedToolRuns(t *testing.T) {
	WithTestRegistry(t, func() {
		registryRuns, offeredRuns := 0, 0
		Registry.Set("cmd", offeredTestTool{name: "cmd", out: "ran from the registry", runs: &registryRuns})
		box := &offeredTestBox{}
		offered := NewOffered()
		offered.Offer(box, offeredTestTool{name: "pwd", out: "/work", runs: &offeredRuns})

		if got := offered.Invoke(t.Context(), call("pwd")); got != "/work" || offeredRuns != 1 {
			t.Fatalf("offered call = %q after %d runs, want /work after 1", got, offeredRuns)
		}
		if got := offered.Invoke(t.Context(), call("cmd")); got != "ERROR: unknown tool call: cmd" {
			t.Fatalf("unoffered call = %q, want the unknown-tool refusal", got)
		}
		if registryRuns != 0 {
			t.Fatalf("the registry's cmd ran %d times, want 0", registryRuns)
		}
		if !slices.Equal(box.registered, offered.Names()) {
			t.Fatalf("box holds %v and the set %v, want the same tools", box.registered, offered.Names())
		}
	})
}

func TestOffered_reofferRegistersOnceAndReplacesTheInstance(t *testing.T) {
	box := &offeredTestBox{}
	offered := NewOffered()
	offered.Offer(box, offeredTestTool{name: "cat", out: "first"})
	offered.Offer(box, offeredTestTool{name: "cat", out: "second"})

	if !slices.Equal(box.registered, []string{"cat"}) {
		t.Fatalf("registered = %v, want cat once", box.registered)
	}
	if got := offered.Invoke(t.Context(), call("cat")); got != "second" {
		t.Fatalf("call = %q, want the instance offered last", got)
	}
}

func TestOffered_aliasReachesOnlyAnOfferedTool(t *testing.T) {
	offered := NewOffered()
	offered.Alias("freetext_command", "cmd")

	if got := offered.Invoke(t.Context(), call("freetext_command")); got != "ERROR: unknown tool call: freetext_command" {
		t.Fatalf("alias of an unoffered tool = %q, want the unknown-tool refusal", got)
	}

	offered.Offer(&offeredTestBox{}, offeredTestTool{name: "cmd", out: "ran"})
	if got := offered.Invoke(t.Context(), call("freetext_command")); got != "ran" {
		t.Fatalf("alias of an offered tool = %q, want ran", got)
	}
	if names := offered.Names(); !slices.Equal(names, []string{"cmd"}) {
		t.Fatalf("names = %v, want the alias left out", names)
	}
}

func TestOffered_nilOffersNothing(t *testing.T) {
	var offered *Offered
	if _, ok := offered.Get("cmd"); ok {
		t.Fatal("a nil set resolved cmd")
	}
	if got := offered.Invoke(t.Context(), call("cmd")); got != "ERROR: unknown tool call: cmd" {
		t.Fatalf("call = %q, want the unknown-tool refusal", got)
	}
	if offered.Names() != nil || offered.Clone() != nil {
		t.Fatal("a nil set must list and clone as nil")
	}
}

func TestOffered_cloneIsIndependent(t *testing.T) {
	offered := NewOffered()
	offered.Offer(&offeredTestBox{}, offeredTestTool{name: "cat"})
	clone := offered.Clone()
	clone.Offer(&offeredTestBox{}, offeredTestTool{name: "ls"})
	clone.Alias("list", "ls")

	if names := offered.Names(); !slices.Equal(names, []string{"cat"}) {
		t.Fatalf("original = %v after the clone changed, want [cat]", names)
	}
	if _, ok := offered.Get("list"); ok {
		t.Fatal("the clone's alias reached the original")
	}
	if names := clone.Names(); !slices.Equal(names, []string{"cat", "ls"}) {
		t.Fatalf("clone = %v, want [cat ls]", names)
	}
}

func TestRun_foldsTheErrorAndPassesInputs(t *testing.T) {
	failing := offeredTestTool{name: "boom", err: errors.New("exit 2")}
	if got := Run(t.Context(), failing, call("boom")); got != "ERROR: failed to run tool: boom, error: exit 2" {
		t.Fatalf("failed call = %q", got)
	}

	contextual := &offeredContextTool{name: "ctx"}
	inputs := pub_models.Input{"k": "v"}
	if got := Run(t.Context(), contextual, pub_models.Call{Name: "ctx", Inputs: &inputs}); got != "with context" {
		t.Fatalf("contextual call = %q, want the CallWithContext output", got)
	}
	if contextual.got["k"] != "v" {
		t.Fatalf("inputs = %v, want the call's inputs", contextual.got)
	}
}
