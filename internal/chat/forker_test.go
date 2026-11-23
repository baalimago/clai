package chat

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/asset"
	"github.com/baalimago/clai/internal/utils"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// memPersister records Save calls and keeps what it was given.
type memPersister struct {
	mu      sync.Mutex
	saved   map[string]pub_models.Chat
	calls   []string
	saveErr error
}

func newMemPersister() *memPersister { return &memPersister{saved: map[string]pub_models.Chat{}} }

func (m *memPersister) Save(_ context.Context, c pub_models.Chat) (pub_models.Chat, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, c.ID)
	if m.saveErr != nil {
		return pub_models.Chat{}, m.saveErr
	}
	m.saved[c.ID] = c
	return c, nil
}

func (m *memPersister) Promote(context.Context, pub_models.Chat, RunOutcome) (SavedID, error) {
	return "", errors.New("unused")
}

func (m *memPersister) Load(_ context.Context, path string) (pub_models.Chat, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id := filepath.Base(path)
	id = id[:len(id)-len(filepath.Ext(id))]
	c, ok := m.saved[id]
	if !ok {
		return pub_models.Chat{}, ErrChatNotFound
	}
	return c, nil
}

func (m *memPersister) Delete(string) (asset.SweepCount, error) { return 0, nil }

type forkFixture struct {
	forker Forker
	// load returns the fork's chat as persisted.
	load func(t *testing.T, id string) pub_models.Chat
	// disk is the conversations directory, "" for a fake persister.
	disk string
	// persister is the fake persister, nil for the real one.
	persister *memPersister
}

type forkerImpl struct {
	name string
	make func(t *testing.T, newID func() (string, error), now func() time.Time) forkFixture
}

func forkerImpls() []forkerImpl {
	return []forkerImpl{
		{"real", func(t *testing.T, newID func() (string, error), now func() time.Time) forkFixture {
			confDir := t.TempDir()
			if err := utils.CreateConfigDir(confDir); err != nil {
				t.Fatalf("CreateConfigDir: %v", err)
			}
			convDir := conversationsDir(confDir)
			p := convPersister(convDir)
			return forkFixture{
				forker: NewForker(p, newID, now),
				disk:   convDir,
				load: func(t *testing.T, id string) pub_models.Chat {
					t.Helper()
					c, err := p.Load(context.Background(), conversationPathFromDir(convDir, id))
					if err != nil {
						t.Fatalf("load %s: %v", id, err)
					}
					return c
				},
			}
		}},
		{"fake", func(t *testing.T, newID func() (string, error), now func() time.Time) forkFixture {
			p := newMemPersister()
			return forkFixture{
				forker:    NewForker(p, newID, now),
				persister: p,
				load: func(t *testing.T, id string) pub_models.Chat {
					t.Helper()
					c, err := p.Load(context.Background(), id+".json")
					if err != nil {
						t.Fatalf("load %s: %v", id, err)
					}
					return c
				},
			}
		}},
	}
}

func seqIDs() func() (string, error) {
	var mu sync.Mutex
	n := 0
	return func() (string, error) {
		mu.Lock()
		defer mu.Unlock()
		n++
		return fmt.Sprintf("fork-%02d", n), nil
	}
}

func forkParent() pub_models.Chat {
	t0 := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	return pub_models.Chat{
		ID: "parent", Created: t0, Profile: "prof", OriginDir: "/work", GroupKey: "gk", Title: "T", Summary: "S", SummaryAt: t0.Add(time.Minute),
		Messages: []pub_models.Message{
			{Role: "system", Content: "sys"},
			{Role: "user", Content: "q1"},
			{Role: "assistant", Content: "a1"},
			{Role: "user", Content: "q2"},
		},
		TokenUsage:       &pub_models.Usage{TotalTokens: 30},
		RecentTokenUsage: &pub_models.Usage{TotalTokens: 12},
		Queries: []pub_models.QueryCost{
			{CreatedAt: t0.Add(time.Minute), Model: "m1", CostUSD: 0.1, Usage: pub_models.Usage{TotalTokens: 10}},
			{CreatedAt: t0.Add(2 * time.Minute), Model: "m2", CostUSD: 0.2, Usage: pub_models.Usage{TotalTokens: 20}},
		},
	}
}

func forkNow() time.Time { return time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC) }

func TestForker_stampsAndCopies(t *testing.T) {
	for _, impl := range forkerImpls() {
		t.Run(impl.name, func(t *testing.T) {
			fx := impl.make(t, seqIDs(), forkNow)
			parent := forkParent()
			got, err := fx.forker.Fork(context.Background(), parent)
			if err != nil {
				t.Fatalf("Fork: %v", err)
			}
			if got.ID != "fork-01" || !got.Created.Equal(forkNow()) || got.Source != "clai" || got.SourceID != "parent" {
				t.Fatalf("stamps: %+v", got)
			}
			disk := fx.load(t, got.ID)
			for _, c := range []pub_models.Chat{got, disk} {
				if !c.Created.Equal(forkNow()) || c.ID != "fork-01" || c.Source != "clai" || c.SourceID != "parent" {
					t.Fatalf("stamps: %+v", c)
				}
				if !reflect.DeepEqual(c.Queries, parent.Queries) || !reflect.DeepEqual(c.Messages, parent.Messages) ||
					c.Title != "T" || c.Summary != "S" || !c.SummaryAt.Equal(parent.SummaryAt) || c.Profile != "prof" ||
					c.OriginDir != "/work" || c.GroupKey != "gk" ||
					!reflect.DeepEqual(c.TokenUsage, parent.TokenUsage) || !reflect.DeepEqual(c.RecentTokenUsage, parent.RecentTokenUsage) {
					t.Fatalf("copy differs:\n got %+v\nwant %+v", c, parent)
				}
			}
			got.Messages[0].Content = "changed"
			got.Queries[0].Model = "changed"
			got.TokenUsage.TotalTokens = -1
			got.RecentTokenUsage.TotalTokens = -1
			if !reflect.DeepEqual(parent, forkParent()) {
				t.Fatalf("mutating the fork changed the parent: %+v", parent)
			}
		})
	}
}

func TestForker_copiesNestedSlices(t *testing.T) {
	parent := forkParent()
	parent.Messages = append(parent.Messages, pub_models.Message{
		Role: "assistant", ToolCalls: []pub_models.Call{{ID: "c1", Name: "x"}},
		ReasoningItems: []pub_models.ReasoningItem{{ID: "r1"}},
		Parts:          []pub_models.Part{pub_models.ImagePart{Asset: "a.png"}},
	})
	fx := forkerImpls()[1].make(t, seqIDs(), forkNow)
	got, err := fx.forker.Fork(context.Background(), parent)
	if err != nil {
		t.Fatalf("Fork: %v", err)
	}
	last := got.Messages[len(got.Messages)-1]
	last.ToolCalls[0].ID = "x"
	last.ReasoningItems[0].ID = "x"
	last.Parts[0] = pub_models.ImagePart{Asset: "x"}
	orig := parent.Messages[len(parent.Messages)-1]
	if orig.ToolCalls[0].ID != "c1" || orig.ReasoningItems[0].ID != "r1" || orig.Parts[0].(pub_models.ImagePart).Asset != "a.png" {
		t.Fatalf("nested slice shared with the parent: %+v", orig)
	}
}

func fileDigest(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return fmt.Sprintf("%x", sha256.Sum256(b))
}

func indexRowOf(t *testing.T, convDir, id string) chatIndexRow {
	t.Helper()
	p, err := NewChatIndexPaginator(context.Background(), convDir)
	if err != nil {
		t.Fatalf("index: %v", err)
	}
	for _, r := range p.rows {
		if r.ID == id {
			return r
		}
	}
	t.Fatalf("no index row for %s", id)
	return chatIndexRow{}
}

func TestForker_neverWritesTheParent(t *testing.T) {
	t.Run("real: file, index row and mtime are unchanged", func(t *testing.T) {
		fx := forkerImpls()[0].make(t, seqIDs(), forkNow)
		parent := forkParent()
		if _, err := convPersister(fx.disk).Save(context.Background(), parent); err != nil {
			t.Fatalf("save: %v", err)
		}
		path := conversationPathFromDir(fx.disk, "parent")
		before := fileDigest(t, path)
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		rowBefore := indexRowOf(t, fx.disk, "parent")
		if _, err := fx.forker.Fork(context.Background(), parent); err != nil {
			t.Fatalf("Fork: %v", err)
		}
		after, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if fileDigest(t, path) != before || !after.ModTime().Equal(info.ModTime()) {
			t.Fatal("parent file changed")
		}
		if !reflect.DeepEqual(rowBefore, indexRowOf(t, fx.disk, "parent")) {
			t.Fatal("parent index row changed")
		}
	})
	t.Run("fake: Save is never called with the parent's ID", func(t *testing.T) {
		fx := forkerImpls()[1].make(t, seqIDs(), forkNow)
		if _, err := fx.forker.Fork(context.Background(), forkParent()); err != nil {
			t.Fatalf("Fork: %v", err)
		}
		for _, id := range fx.persister.calls {
			if id == "parent" {
				t.Fatal("Save called with the parent's ID")
			}
		}
	})
}

func TestForker_concurrentForks(t *testing.T) {
	fx := forkerImpls()[0].make(t, chatIDForTest, forkNow)
	parent := forkParent()
	if _, err := convPersister(fx.disk).Save(context.Background(), parent); err != nil {
		t.Fatalf("save: %v", err)
	}
	const n = 16
	ids := make(chan string, n)
	errs := make(chan error, n)
	var wg sync.WaitGroup
	for range n {
		wg.Go(func() {
			c, err := fx.forker.Fork(context.Background(), parent)
			if err != nil {
				errs <- err
				return
			}
			ids <- c.ID
		})
	}
	wg.Wait()
	close(ids)
	close(errs)
	for err := range errs {
		t.Fatalf("Fork: %v", err)
	}
	seen := map[string]bool{}
	for id := range ids {
		if seen[id] {
			t.Fatalf("duplicate ID %s", id)
		}
		seen[id] = true
		if c := fx.load(t, id); len(c.Messages) != len(parent.Messages) {
			t.Fatalf("fork %s incomplete", id)
		}
		indexRowOf(t, fx.disk, id)
	}
	if len(seen) != n {
		t.Fatalf("got %d forks, want %d", len(seen), n)
	}
}

var chatIDCounter struct {
	sync.Mutex
	n int
}

func chatIDForTest() (string, error) {
	chatIDCounter.Lock()
	defer chatIDCounter.Unlock()
	chatIDCounter.n++
	return fmt.Sprintf("conc-%03d", chatIDCounter.n), nil
}

func dirNames(t *testing.T, dir string) []string {
	t.Helper()
	es, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir %s: %v", dir, err)
	}
	var names []string
	for _, e := range es {
		names = append(names, e.Name())
	}
	return names
}

func TestForker_sidecarAndImages(t *testing.T) {
	fx := forkerImpls()[0].make(t, seqIDs(), forkNow)
	p := convPersister(fx.disk)
	ref, err := convAssetStore(fx.disk).Put(testPNG(t, 7), "image/png")
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	parent := forkParent()
	parent.Messages = append(parent.Messages,
		pub_models.Message{Role: "user", Parts: []pub_models.Part{pub_models.ImagePart{Asset: ref, MIME: "image/png", Origin: pub_models.OriginUser}}},
		pub_models.Message{Role: "assistant", ToolCalls: []pub_models.Call{{ID: "call1", Name: "x"}}, ReasoningItems: []pub_models.ReasoningItem{{ID: "r1", EncryptedContent: "enc"}}},
	)
	if _, err := p.Save(context.Background(), parent); err != nil {
		t.Fatalf("save parent: %v", err)
	}
	assetsBefore := dirNames(t, assetsDirFromConvDir(fx.disk))
	loaded, err := p.Load(context.Background(), conversationPathFromDir(fx.disk, "parent"))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	got, err := fx.forker.Fork(context.Background(), loaded)
	if err != nil {
		t.Fatalf("Fork: %v", err)
	}
	if !reflect.DeepEqual(assetsBefore, dirNames(t, assetsDirFromConvDir(fx.disk))) {
		t.Fatal("fork copied or added an asset")
	}
	want, err := os.ReadFile(reasoningFileFromConvDir(fx.disk, "parent", "call1"))
	if err != nil {
		t.Fatalf("parent sidecar: %v", err)
	}
	have, err := os.ReadFile(reasoningFileFromConvDir(fx.disk, got.ID, "call1"))
	if err != nil {
		t.Fatalf("fork sidecar: %v", err)
	}
	if !bytes.Equal(want, have) {
		t.Fatalf("sidecar differs: %s vs %s", want, have)
	}
	reloaded := fx.load(t, got.ID)
	img, ok := reloaded.Messages[len(reloaded.Messages)-2].Parts[0].(pub_models.ImagePart)
	if !ok || img.Asset != ref {
		t.Fatalf("image ref not shared: %+v", reloaded.Messages[len(reloaded.Messages)-2])
	}
}

func TestForker_ownSpendStartsAtZero(t *testing.T) {
	for _, impl := range forkerImpls() {
		t.Run(impl.name, func(t *testing.T) {
			fx := impl.make(t, seqIDs(), forkNow)
			parent := forkParent()
			got, err := fx.forker.Fork(context.Background(), parent)
			if err != nil {
				t.Fatalf("Fork: %v", err)
			}
			if s := chatOwnSpend(fx.load(t, got.ID)); s != (OwnSpend{}) {
				t.Fatalf("fork own spend = %+v, want zero", s)
			}
			if s := chatOwnSpend(parent); s.Tokens != 30 {
				t.Fatalf("parent own spend = %+v, want 30 tokens", s)
			}
			if len(got.Queries) != 2 {
				t.Fatalf("queries not copied: %+v", got.Queries)
			}
		})
	}
}

func TestForker_emptyParentID(t *testing.T) {
	for _, impl := range forkerImpls() {
		t.Run(impl.name, func(t *testing.T) {
			fx := impl.make(t, seqIDs(), forkNow)
			_, err := fx.forker.Fork(context.Background(), pub_models.Chat{})
			if !errors.Is(err, ErrForkParentUnsaved) {
				t.Fatalf("err = %v, want ErrForkParentUnsaved", err)
			}
			if fx.persister != nil && len(fx.persister.calls) != 0 {
				t.Fatal("Save was called")
			}
			if fx.disk != "" {
				es, _ := os.ReadDir(fx.disk)
				for _, e := range es {
					if filepath.Ext(e.Name()) == ".json" {
						t.Fatalf("wrote %s", e.Name())
					}
				}
			}
		})
	}
}

func TestForker_persisterFailure(t *testing.T) {
	p := newMemPersister()
	p.saveErr = errors.New("disk full")
	_, err := NewForker(p, seqIDs(), forkNow).Fork(context.Background(), forkParent())
	var fe *ForkError
	if !errors.As(err, &fe) || fe.ParentID != "parent" || !errors.Is(err, p.saveErr) {
		t.Fatalf("err = %v, want *ForkError wrapping the cause", err)
	}
	if fe.Error() != "fork chat parent: disk full" {
		t.Fatalf("Error() = %q", fe.Error())
	}
}

func TestForker_idFailure(t *testing.T) {
	p := newMemPersister()
	cause := errors.New("no entropy")
	_, err := NewForker(p, func() (string, error) { return "", cause }, forkNow).Fork(context.Background(), forkParent())
	var fe *ForkError
	if !errors.As(err, &fe) || !errors.Is(err, cause) {
		t.Fatalf("err = %v, want *ForkError wrapping the cause", err)
	}
	if len(p.calls) != 0 {
		t.Fatal("saved after an ID failure")
	}
}
