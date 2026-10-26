package cloud

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// fakeServer is clai.chat's /api/v1 in memory: one account, the stores'
// compare-and-swap commits, and blobs by hash.
type fakeServer struct {
	t  *testing.T
	mu sync.Mutex
	// token → device name
	tokens  map[string]string
	blobs   map[string][]byte
	entries map[Store]map[string]RemoteEntry
	rev     map[Store]int64
	// conversations answers 402 unless subscribed.
	subscribed bool
	pageSize   int
	// device login
	pollsLeft  int
	denyLogin  bool
	deviceName string
	revoked    bool
	srv        *httptest.Server
	// onCommit runs once, under the lock, before the next commit: another
	// device committing between our pull and our push.
	onCommit func()
}

func newFakeServer(t *testing.T) *fakeServer {
	t.Helper()
	f := &fakeServer{
		t:          t,
		tokens:     map[string]string{"tok-a": "a", "tok-b": "b"},
		blobs:      map[string][]byte{},
		entries:    map[Store]map[string]RemoteEntry{StoreConfig: {}, StoreConversations: {}},
		rev:        map[Store]int64{},
		subscribed: true,
		pageSize:   1000,
	}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeServer) client(token string) *Client { return NewClient(f.srv.URL, token) }

var blobPath = regexp.MustCompile(`^/api/v1/blobs/([0-9a-f]{64})$`)

func problem(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": code, "message": msg})
}

func reply(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (f *fakeServer) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch r.URL.Path {
	case "/api/v1/device/code":
		var in map[string]string
		_ = json.NewDecoder(r.Body).Decode(&in)
		f.deviceName = in["name"]
		reply(w, 200, DeviceCode{DeviceCode: "dc", UserCode: "ABCD-EFGH", VerificationURI: f.srv.URL + "/device", VerificationURIComplete: f.srv.URL + "/device?code=ABCD-EFGH", ExpiresIn: 600, Interval: 5})
		return
	case "/api/v1/device/token":
		switch {
		case f.denyLogin:
			problem(w, 400, "access_denied", "refused")
		case f.pollsLeft > 0:
			f.pollsLeft--
			problem(w, 400, "authorization_pending", "waiting")
		default:
			f.tokens["tok-new"] = f.deviceName
			reply(w, 200, map[string]string{"access_token": "tok-new", "token_type": "Bearer", "device_name": f.deviceName})
		}
		return
	}
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	device, ok := f.tokens[token]
	if !ok {
		problem(w, 401, "unauthorized", "run clai cloud login")
		return
	}
	if m := blobPath.FindStringSubmatch(r.URL.Path); m != nil {
		if r.Method == http.MethodGet {
			b, ok := f.blobs[m[1]]
			if !ok {
				problem(w, 404, "not_found", "no such blob")
				return
			}
			_, _ = w.Write(b)
			return
		}
		b, _ := io.ReadAll(r.Body)
		if hashBytes(b) != m[1] {
			problem(w, 400, "mismatch", "bad hash")
			return
		}
		f.blobs[m[1]] = b
		reply(w, 201, map[string]string{"sha256": m[1]})
		return
	}
	switch {
	case r.URL.Path == "/api/v1/me":
		stores := map[Store]StoreInfo{}
		for _, s := range Stores {
			stores[s] = StoreInfo{Readable: s == StoreConfig || f.subscribed, Writable: s == StoreConfig || f.subscribed, Rev: f.rev[s], Files: len(f.entries[s])}
		}
		kind := "none"
		if f.subscribed {
			kind = "active"
		}
		reply(w, 200, map[string]any{"account": "u-12345678", "device": device, "billing": true, "conversations": map[string]any{"kind": kind}, "stores": stores})
	case r.URL.Path == "/api/v1/tokens/current":
		delete(f.tokens, token)
		f.revoked = true
		w.WriteHeader(204)
	case r.URL.Path == "/api/v1/blobs/missing":
		var in struct{ Hashes []string }
		_ = json.NewDecoder(r.Body).Decode(&in)
		missing := []string{}
		for _, h := range in.Hashes {
			if _, ok := f.blobs[h]; !ok {
				missing = append(missing, h)
			}
		}
		reply(w, 200, map[string][]string{"missing": missing})
	case strings.HasPrefix(r.URL.Path, "/api/v1/stores/"):
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/v1/stores/"), "/")
		store := Store(parts[0])
		if store == StoreConversations && !f.subscribed {
			problem(w, 402, "subscription_required", "the conversation store needs a subscription")
			return
		}
		if parts[1] == "changes" {
			f.changes(w, store, r.URL.Query().Get("since"))
			return
		}
		f.commit(w, r, store, device)
	default:
		problem(w, 404, "not_found", r.URL.Path)
	}
}

func (f *fakeServer) changes(w http.ResponseWriter, store Store, sinceStr string) {
	since, _ := strconv.ParseInt(sinceStr, 10, 64)
	var out []RemoteEntry
	for rev := since + 1; rev <= f.rev[store]; rev++ {
		for _, e := range f.entries[store] {
			if e.Rev == rev {
				out = append(out, e)
			}
		}
	}
	more := len(out) > f.pageSize
	if more {
		out = out[:f.pageSize]
	}
	cursor := since
	if len(out) > 0 {
		cursor = out[len(out)-1].Rev
	}
	if out == nil {
		out = []RemoteEntry{}
	}
	reply(w, 200, ChangesPage{Entries: out, Cursor: cursor, More: more})
}

func (f *fakeServer) commit(w http.ResponseWriter, r *http.Request, store Store, device string) {
	if f.onCommit != nil {
		hook := f.onCommit
		f.onCommit = nil
		hook()
	}
	var in struct{ Changes []Change }
	_ = json.NewDecoder(r.Body).Decode(&in)
	res := map[string]any{}
	applied := []map[string]any{}
	conflicts := []map[string]any{}
	for _, c := range in.Changes {
		if !Allowed(store, c.Path) {
			problem(w, 400, "invalid_request", "path "+c.Path)
			return
		}
		if !c.Deleted {
			if _, ok := f.blobs[c.SHA256]; !ok {
				problem(w, 400, "blob_missing", c.SHA256)
				return
			}
		}
	}
	for _, c := range in.Changes {
		cur, ok := f.entries[store][c.Path]
		if (ok && cur.Rev != c.BaseRev) || (!ok && c.BaseRev != 0) {
			e := cur
			conflicts = append(conflicts, map[string]any{"path": c.Path, "current": &e})
			continue
		}
		f.rev[store]++
		e := RemoteEntry{Path: c.Path, SHA256: c.SHA256, Size: c.Size, Rev: f.rev[store], Deleted: c.Deleted, Device: device}
		if c.Deleted {
			e.SHA256 = ""
		}
		f.entries[store][c.Path] = e
		applied = append(applied, map[string]any{"path": c.Path, "rev": e.Rev})
	}
	res["rev"] = f.rev[store]
	res["applied"] = applied
	res["conflicts"] = conflicts
	reply(w, 200, res)
}

// put stores a file as if another device had committed it.
func (f *fakeServer) put(store Store, path string, content string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.putLocked(store, path, content)
}

func (f *fakeServer) putLocked(store Store, path string, content string) {
	sha := hashBytes([]byte(content))
	f.blobs[sha] = []byte(content)
	f.rev[store]++
	f.entries[store][path] = RemoteEntry{Path: path, SHA256: sha, Size: int64(len(content)), Rev: f.rev[store], Device: "other"}
}

func (f *fakeServer) del(store Store, path string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rev[store]++
	f.entries[store][path] = RemoteEntry{Path: path, Rev: f.rev[store], Deleted: true, Device: "other"}
}

func (f *fakeServer) content(store Store, path string) (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	e, ok := f.entries[store][path]
	if !ok || e.Deleted {
		return "", false
	}
	return string(f.blobs[e.SHA256]), true
}
