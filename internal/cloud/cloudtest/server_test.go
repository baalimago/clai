package cloudtest

import (
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/baalimago/clai/internal/testenv"
)

func TestMain(m *testing.M) { testenv.Main(m) }

func do(t *testing.T, s *Server, method, path, body string) {
	t.Helper()
	req, err := http.NewRequest(method, s.URL()+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer tok-a")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, res.Body)
	_ = res.Body.Close()
}

func TestServer_recordsFetchedHashes(t *testing.T) {
	s := New(t)
	s.Put("conversations", "conversations/a.json", "a")
	s.Put("conversations", "conversations/b.json", "b")
	s.Put("conversations", "conversations/c.json", "c")
	ha, hb, hc := Hash([]byte("a")), Hash([]byte("b")), Hash([]byte("c"))

	if len(s.FetchedHashes) != 0 {
		t.Fatalf("untouched server holds %v", s.FetchedHashes)
	}
	do(t, s, http.MethodPost, "/api/v1/blobs/fetch", `{"hashes":["`+hb+`","`+ha+`"]}`)
	do(t, s, http.MethodGet, "/api/v1/blobs/"+hc, "")
	do(t, s, http.MethodPost, "/api/v1/blobs/missing", `{"hashes":["`+ha+`"]}`)
	do(t, s, http.MethodGet, "/api/v1/stores/conversations/changes?since=0", "")

	if want := []string{hb, ha, hc}; !slices.Equal(s.FetchedHashes, want) {
		t.Fatalf("FetchedHashes = %v, want %v", s.FetchedHashes, want)
	}
}
