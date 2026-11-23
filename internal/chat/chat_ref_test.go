package chat

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"reflect"
	"strings"
	"testing"

	"github.com/baalimago/clai/internal/testenv/procenv"
	"github.com/baalimago/clai/pkg/diagnostic"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

type fakeResolver struct {
	res   CloudResolution
	err   error
	calls []string
}

func (f *fakeResolver) ResolveCloud(_ context.Context, id string) (CloudResolution, error) {
	f.calls = append(f.calls, id)
	return f.res, f.err
}

func Test_parseChatRef(t *testing.T) {
	for token, want := range map[string]ChatRef{
		"cc:abc": {true, "abc"},
		"cc:":    {true, ""},
		"abc":    {false, "abc"},
		"3":      {false, "3"},
		"cc:3":   {true, "3"},
		"CC:abc": {false, "CC:abc"},
	} {
		if got := ParseChatRef(token); got != want {
			t.Errorf("ParseChatRef(%q) = %+v, want %+v", token, got, want)
		}
	}
}

func Test_chatRef_localBranch(t *testing.T) {
	for _, token := range []string{"2 go on", "newest go on", "nope", "9"} {
		t.Run(token, func(t *testing.T) {
			viaRef := continueIndexFixture(t)
			resolver := &fakeResolver{}
			viaRef.cloudResolver = func(context.Context, string) (CloudResolver, error) { return resolver, nil }
			viaID := continueIndexFixture(t)
			gotRef, errRef := viaRef.findChatByRef(context.Background(), token, foreignClone)
			gotID, errID := viaID.findChatByID(context.Background(), token, foreignClone)
			if (errRef == nil) != (errID == nil) || (errRef != nil && errors.Is(errRef, fs.ErrNotExist) != errors.Is(errID, fs.ErrNotExist)) {
				t.Fatalf("errors differ: %v vs %v", errRef, errID)
			}
			if gotRef.ID != gotID.ID || viaRef.prompt != viaID.prompt {
				t.Fatalf("got %q/%q, want %q/%q", gotRef.ID, viaRef.prompt, gotID.ID, viaID.prompt)
			}
			if len(resolver.calls) != 0 {
				t.Fatal("resolver called for a local token")
			}
		})
	}
}

func Test_chatRef_cloudBranch(t *testing.T) {
	t.Run("resolves, reports the notice once and keeps the prompt", func(t *testing.T) {
		cq, _ := newTestHandler(t)
		cq.subCmd = "continue"
		resolver := &fakeResolver{res: CloudResolution{Chat: pub_models.Chat{ID: "X"}, Notice: "n"}}
		cq.cloudResolver = func(context.Context, string) (CloudResolver, error) { return resolver, nil }
		var stderr bytes.Buffer
		ctx := procenv.Diagnostics(context.Background(), &stderr, diagnostic.LevelInfo)
		got, err := cq.findChatByRef(ctx, "cc:X do it", foreignClone)
		if err != nil {
			t.Fatalf("findChatByRef: %v", err)
		}
		if got.ID != "X" || cq.prompt != "do it" || !reflect.DeepEqual(resolver.calls, []string{"X"}) {
			t.Fatalf("got %q prompt %q calls %v", got.ID, cq.prompt, resolver.calls)
		}
		if strings.Count(stderr.String(), "n\n") != 1 {
			t.Fatalf("stderr = %q, want the notice once", stderr.String())
		}
	})
	t.Run("a resolver error is a CloudRefError and never the list UI", func(t *testing.T) {
		cq, _ := newTestHandler(t)
		cq.subCmd = "continue"
		cause := errors.New("login first")
		cq.cloudResolver = func(context.Context, string) (CloudResolver, error) { return &fakeResolver{err: cause}, nil }
		var out bytes.Buffer
		cq.out = &out
		cq.prompt = "cc:X hi"
		err := cq.cont(context.Background())
		var refErr *CloudRefError
		if !errors.As(err, &refErr) || refErr.ChatID != "X" || !errors.Is(err, cause) || err.Error() != "login first" {
			t.Fatalf("err = %v", err)
		}
		if out.Len() != 0 {
			t.Fatalf("list UI printed %q", out.String())
		}
	})
	t.Run("a factory error is a CloudRefError", func(t *testing.T) {
		cq, _ := newTestHandler(t)
		cause := errors.New("no token")
		cq.cloudResolver = func(context.Context, string) (CloudResolver, error) { return nil, cause }
		_, err := cq.findChatByRef(context.Background(), "cc:X", foreignClone)
		var refErr *CloudRefError
		if !errors.As(err, &refErr) || !errors.Is(err, cause) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("fork without a prompt never reaches the resolver", func(t *testing.T) {
		fr := forkFixtureHandler(t, "cc:X", savedParent())
		resolver := &fakeResolver{}
		fr.cq.cloudResolver = func(context.Context, string) (CloudResolver, error) { return resolver, nil }
		err := fr.cq.fork(context.Background())
		if err == nil || err.Error() != forkPromptRefusal || len(resolver.calls) != 0 {
			t.Fatalf("err = %v calls %v", err, resolver.calls)
		}
	})
	t.Run("fork sends the prompt to a copy of the resolved chat", func(t *testing.T) {
		fr := forkFixtureHandler(t, "cc:parent try this", savedParent())
		resolved := forkParent()
		fr.cq.cloudResolver = func(context.Context, string) (CloudResolver, error) {
			return &fakeResolver{res: CloudResolution{Chat: resolved}}, nil
		}
		if err := fr.cq.fork(context.Background()); err != nil {
			t.Fatalf("fork: %v", err)
		}
		if fr.sent[0].Prompt != "try this" || fr.sent[0].Profile != "prof" {
			t.Fatalf("sent %+v", fr.sent[0])
		}
	})
	t.Run("fork returns a resolver failure as it is", func(t *testing.T) {
		fr := forkFixtureHandler(t, "cc:X hi", savedParent())
		before := convSnapshot(t, fr.convDir)
		fr.cq.cloudResolver = func(context.Context, string) (CloudResolver, error) {
			return &fakeResolver{err: errors.New("offline")}, nil
		}
		err := fr.cq.fork(context.Background())
		var refErr *CloudRefError
		if !errors.As(err, &refErr) || err.Error() != "offline" {
			t.Fatalf("err = %v", err)
		}
		if !reflect.DeepEqual(before, convSnapshot(t, fr.convDir)) {
			t.Fatal("wrote")
		}
	})
}

func Test_chatRef_refusals(t *testing.T) {
	failing := func(context.Context, string) (CloudResolver, error) { return nil, errors.New("factory failed") }
	for _, tc := range []struct {
		name    string
		verb    string
		prompt  string
		factory func(context.Context, string) (CloudResolver, error)
		want    string
		wantIs  error
	}{
		{"continue cc:", "continue", "cc:", failing, `chat continue: "cc:" needs a conversation ID, as in cc:<chatID>`, nil},
		{"fork cc:", "fork", "cc: hi", failing, `chat fork: "cc:" needs a conversation ID, as in cc:<chatID>`, nil},
		{"delete cc:X", "delete", "cc:X", failing, "chat delete: cc: references are for continue and fork; delete the local copy with 'clai chat delete X'", nil},
		{"continue nil factory", "continue", "cc:X hi", nil, "chat continue: clai.chat references are not available in this build", ErrCloudRefsUnavailable},
		{"fork nil factory", "fork", "cc:X p", nil, "chat fork: clai.chat references are not available in this build", ErrCloudRefsUnavailable},
		{"continue failing factory", "continue", "cc:X hi", failing, "factory failed", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fr := forkFixtureHandler(t, tc.prompt, savedParent())
			fr.cq.subCmd = tc.verb
			fr.cq.cloudResolver = tc.factory
			before := convSnapshot(t, fr.convDir)
			err := fr.cq.Query(context.Background())
			if err == nil || err.Error() != tc.want {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
			if tc.wantIs != nil && !errors.Is(err, tc.wantIs) {
				t.Fatalf("err = %v, want %v in the chain", err, tc.wantIs)
			}
			if !reflect.DeepEqual(before, convSnapshot(t, fr.convDir)) || len(fr.sent) != 0 {
				t.Fatal("wrote, deleted or sent")
			}
		})
	}
}
