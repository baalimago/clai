// Package cloudtest is clai.chat's /api/v1 in memory, for the tests of
// internal/cloud and the packages that drive it.
package cloudtest

import (
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// Entry is one path's state on the server, as the changes feed sends it.
type Entry struct {
	Path      string `json:"path"`
	SHA256    string `json:"sha256"`
	Size      int64  `json:"size"`
	Rev       int64  `json:"rev"`
	Deleted   bool   `json:"deleted"`
	Device    string `json:"device"`
	Commit    int64  `json:"commit"`
	Message   string `json:"message"`
	UpdatedAt int64  `json:"updated_at"`
}

type logEntry struct {
	Commit    int64  `json:"commit"`
	Message   string `json:"message"`
	Device    string `json:"device"`
	Files     int    `json:"files"`
	Bytes     int64  `json:"bytes"`
	CreatedAt int64  `json:"created_at"`
}

type change struct {
	Path    string `json:"path"`
	BaseRev int64  `json:"base_rev"`
	SHA256  string `json:"sha256"`
	Size    int64  `json:"size"`
	Deleted bool   `json:"deleted"`
}

// Server is one account with two stores. Its fields are read and set by
// tests under Lock, or before the first request.
type Server struct {
	mu      sync.Mutex
	Tokens  map[string]string
	Blobs   map[string][]byte
	Entries map[string]map[string]Entry
	Rev     map[string]int64
	Commits map[string][]logEntry
	// PageSize caps a changes page.
	PageSize int
	// Refuse answers every commit with this error, such as card_required.
	Refuse *Problem
	// RefuseBlobs answers every blob upload with this error.
	RefuseBlobs *Problem
	// NoBatch answers /api/v1/blobs/batch and /api/v1/blobs/fetch 404, as
	// a server from before them.
	NoBatch bool
	// GzippedBatches counts the batches that arrived gzipped.
	GzippedBatches int
	// CorruptFetch flips a byte of every blob a fetch bundle carries.
	CorruptFetch bool
	// BatchOutcome, when set, is every batch blob's answer instead of
	// stored, such as mismatch.
	BatchOutcome string
	// CardOnFile and Overdue are the account's billing standing in /me.
	CardOnFile bool
	Overdue    bool
	// BillingClosed answers billing_open false, as a server without Stripe.
	BillingClosed bool
	// PollsLeft answers authorization_pending this many times; Deny refuses
	// the login; SlowDown answers slow_down once.
	PollsLeft  int
	Deny       bool
	SlowDown   bool
	DeviceName string
	Revoked    bool
	// OnCommit runs once, before the next commit is checked: another
	// device committing between a pull and a commit.
	OnCommit func(*Server)
	// Requests counts calls per path, without the query.
	Requests map[string]int
	// Clock stamps commits, in Unix milliseconds.
	Clock int64
	srv   *httptest.Server
}

// Problem is an error answer.
type Problem struct {
	Status  int
	Code    string
	Message string
	URL     string
}

// New starts a server with two device tokens, tok-a and tok-b.
func New(t testing.TB) *Server {
	t.Helper()
	s := &Server{
		Tokens:   map[string]string{"tok-a": "a", "tok-b": "b"},
		Blobs:    map[string][]byte{},
		Entries:  map[string]map[string]Entry{"config": {}, "conversations": {}},
		Rev:      map[string]int64{},
		Commits:  map[string][]logEntry{},
		PageSize: 1000,
		Requests: map[string]int{},
		Clock:    1_700_000_000_000,
		// Most tests commit conversations; billing tests take the card away.
		CardOnFile: true,
	}
	s.srv = httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.srv.Close)
	return s
}

// URL is the server's origin.
func (s *Server) URL() string { return s.srv.URL }

// Close stops the server, so later calls fail as if offline.
func (s *Server) Close() { s.srv.Close() }

// Hash names content the way the server does.
func Hash(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

var blobPath = regexp.MustCompile(`^/api/v1/blobs/([0-9a-f]{64})$`)

func problem(w http.ResponseWriter, p Problem) {
	reply(w, p.Status, map[string]string{"error": p.Code, "message": p.Message, "url": p.URL})
}

func reply(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Requests[r.URL.Path]++
	switch r.URL.Path {
	case "/cli/v1/start":
		var in map[string]string
		_ = json.NewDecoder(r.Body).Decode(&in)
		s.DeviceName = in["client"]
		reply(w, 200, map[string]any{"deviceCode": "dc", "userCode": "BCDF-GHJK", "verificationUri": s.srv.URL + "/cli/login", "verificationUriComplete": s.srv.URL + "/cli/login#BCDF-GHJK", "expiresIn": 600, "interval": 5})
		return
	case "/cli/v1/token":
		s.token(w)
		return
	}
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	device, ok := s.Tokens[token]
	if !ok {
		problem(w, Problem{Status: 401, Code: "unauthorized", Message: "token revoked"})
		return
	}
	if m := blobPath.FindStringSubmatch(r.URL.Path); m != nil {
		s.blob(w, r, m[1])
		return
	}
	switch {
	case r.URL.Path == "/api/v1/me":
		s.me(w, device)
	case r.URL.Path == "/api/v1/tokens/current" && r.Method == http.MethodDelete:
		delete(s.Tokens, token)
		s.Revoked = true
		w.WriteHeader(204)
	case r.URL.Path == "/api/v1/blobs/batch" && r.Method == http.MethodPost && !s.NoBatch:
		s.batch(w, r)
	case r.URL.Path == "/api/v1/blobs/fetch" && r.Method == http.MethodPost && !s.NoBatch:
		s.fetch(w, r)
	case r.URL.Path == "/api/v1/blobs/missing":
		var in struct{ Hashes []string }
		_ = json.NewDecoder(r.Body).Decode(&in)
		missing := []string{}
		for _, h := range in.Hashes {
			if _, ok := s.Blobs[h]; !ok {
				missing = append(missing, h)
			}
		}
		reply(w, 200, map[string][]string{"missing": missing})
	case strings.HasPrefix(r.URL.Path, "/api/v1/stores/"):
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/v1/stores/"), "/")
		if _, ok := s.Entries[parts[0]]; !ok || len(parts) != 2 {
			problem(w, Problem{Status: 404, Code: "not_found", Message: r.URL.Path})
			return
		}
		switch parts[1] {
		case "changes":
			since, _ := strconv.ParseInt(r.URL.Query().Get("since"), 10, 64)
			s.changes(w, parts[0], since)
		case "log":
			limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
			s.log(w, parts[0], limit)
		case "commit":
			s.commit(w, r, parts[0], device)
		default:
			problem(w, Problem{Status: 404, Code: "not_found", Message: r.URL.Path})
		}
	default:
		problem(w, Problem{Status: 404, Code: "not_found", Message: r.URL.Path})
	}
}

func (s *Server) token(w http.ResponseWriter) {
	switch {
	case s.Deny:
		problem(w, Problem{Status: 400, Code: "access_denied", Message: "refused"})
	case s.SlowDown:
		s.SlowDown = false
		problem(w, Problem{Status: 400, Code: "slow_down", Message: "slow down"})
	case s.PollsLeft > 0:
		s.PollsLeft--
		problem(w, Problem{Status: 400, Code: "authorization_pending", Message: "waiting"})
	default:
		s.Tokens["tok-new"] = s.DeviceName
		reply(w, 200, map[string]any{"key": "tok-new", "access": "write", "expiresAt": "2027-10-26T00:00:00Z", "endpoint": s.srv.URL, "account": "lorentz@example.com"})
	}
}

func (s *Server) blob(w http.ResponseWriter, r *http.Request, sha string) {
	if r.Method == http.MethodGet {
		b, ok := s.Blobs[sha]
		if !ok {
			problem(w, Problem{Status: 404, Code: "not_found", Message: "no such blob"})
			return
		}
		_, _ = w.Write(b)
		return
	}
	b, _ := io.ReadAll(r.Body)
	if s.RefuseBlobs != nil {
		problem(w, *s.RefuseBlobs)
		return
	}
	if Hash(b) != sha {
		problem(w, Problem{Status: 400, Code: "mismatch", Message: "bad hash"})
		return
	}
	s.Blobs[sha] = b
	reply(w, 201, map[string]string{"sha256": sha})
}

func (s *Server) batch(w http.ResponseWriter, r *http.Request) {
	if s.RefuseBlobs != nil {
		problem(w, *s.RefuseBlobs)
		return
	}
	if r.Header.Get("X-Clai-Encoding") == "gzip" {
		zr, err := gzip.NewReader(r.Body)
		if err != nil {
			problem(w, Problem{Status: 400, Code: "invalid_request", Message: err.Error()})
			return
		}
		r.Body = zr
		s.GzippedBatches++
	}
	if err := r.ParseMultipartForm(16 << 20); err != nil {
		problem(w, Problem{Status: 400, Code: "invalid_request", Message: err.Error()})
		return
	}
	results := map[string]string{}
	for sha, files := range r.MultipartForm.File {
		f, err := files[0].Open()
		if err != nil {
			problem(w, Problem{Status: 400, Code: "invalid_request", Message: err.Error()})
			return
		}
		b, _ := io.ReadAll(f)
		_ = f.Close()
		switch {
		case s.BatchOutcome != "":
			results[sha] = s.BatchOutcome
		case Hash(b) != sha:
			results[sha] = "mismatch"
		case s.Blobs[sha] != nil:
			results[sha] = "exists"
		default:
			s.Blobs[sha] = b
			results[sha] = "stored"
		}
	}
	reply(w, 200, map[string]any{"results": results})
}

// fetch answers a gzipped bundle of "<sha256> <size>\n<bytes>" frames.
func (s *Server) fetch(w http.ResponseWriter, r *http.Request) {
	var in struct{ Hashes []string }
	_ = json.NewDecoder(r.Body).Decode(&in)
	missing := []string{}
	for _, h := range in.Hashes {
		if _, ok := s.Blobs[h]; !ok {
			missing = append(missing, h)
		}
	}
	if len(missing) > 0 {
		reply(w, 404, map[string]any{"error": "not_found", "message": "no such blob", "missing": missing})
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("X-Clai-Encoding", "gzip")
	zw := gzip.NewWriter(w)
	for _, h := range in.Hashes {
		b := slices.Clone(s.Blobs[h])
		if s.CorruptFetch && len(b) > 0 {
			b[0] ^= 0xff
		}
		_, _ = fmt.Fprintf(zw, "%s %d\n", h, len(b))
		_, _ = zw.Write(b)
	}
	_ = zw.Close()
}

func (s *Server) me(w http.ResponseWriter, device string) {
	stores := map[string]any{}
	var total int64
	for name, entries := range s.Entries {
		var bytes int64
		files := 0
		for _, e := range entries {
			if !e.Deleted {
				bytes += e.Size
				files++
			}
		}
		total += bytes
		stores[name] = map[string]any{"rev": s.Rev[name], "commit": int64(len(s.Commits[name])), "bytes": bytes, "files": files}
	}
	reply(w, 200, map[string]any{
		"account": "u-12345678", "handle": "lorentz", "device": device, "access": "write",
		"usage": map[string]any{
			"bytes": total, "config_limit_bytes": 1_000_000_000, "card_on_file": s.CardOnFile, "overdue": s.Overdue, "billing_open": !s.BillingClosed,
			"billing_url": s.srv.URL + "/account/billing/",
		},
		"stores": stores,
	})
}

func (s *Server) changes(w http.ResponseWriter, store string, since int64) {
	var out []Entry
	for _, e := range s.Entries[store] {
		if e.Rev > since {
			out = append(out, e)
		}
	}
	slices.SortFunc(out, func(a, b Entry) int { return int(a.Rev - b.Rev) })
	more := len(out) > s.PageSize
	if more {
		out = out[:s.PageSize]
	}
	cursor := since
	if len(out) > 0 {
		cursor = out[len(out)-1].Rev
	}
	if out == nil {
		out = []Entry{}
	}
	reply(w, 200, map[string]any{"entries": out, "cursor": cursor, "more": more})
}

func (s *Server) log(w http.ResponseWriter, store string, limit int) {
	all := s.Commits[store]
	out := []logEntry{}
	for i := len(all) - 1; i >= 0 && (limit <= 0 || len(out) < limit); i-- {
		out = append(out, all[i])
	}
	reply(w, 200, map[string]any{"commits": out})
}

func (s *Server) commit(w http.ResponseWriter, r *http.Request, store, device string) {
	if s.OnCommit != nil {
		hook := s.OnCommit
		s.OnCommit = nil
		hook(s)
	}
	if s.Refuse != nil {
		problem(w, *s.Refuse)
		return
	}
	var in struct {
		Message string   `json:"message"`
		Changes []change `json:"changes"`
	}
	_ = json.NewDecoder(r.Body).Decode(&in)
	conflicts := []map[string]any{}
	for _, c := range in.Changes {
		if !c.Deleted {
			if _, ok := s.Blobs[c.SHA256]; !ok {
				problem(w, Problem{Status: 400, Code: "blob_missing", Message: c.SHA256})
				return
			}
		}
		cur, ok := s.Entries[store][c.Path]
		if (ok && cur.Rev != c.BaseRev) || (!ok && c.BaseRev != 0) {
			e := cur
			conflicts = append(conflicts, map[string]any{"path": c.Path, "current": &e})
		}
	}
	if len(conflicts) > 0 {
		reply(w, 200, map[string]any{"commit": 0, "rev": s.Rev[store], "applied": []any{}, "conflicts": conflicts})
		return
	}
	applied := []map[string]any{}
	files := make([]Entry, 0, len(in.Changes))
	for _, c := range in.Changes {
		files = append(files, Entry{Path: c.Path, SHA256: c.SHA256, Size: c.Size, Deleted: c.Deleted})
	}
	commit := s.apply(store, device, in.Message, files)
	for _, c := range in.Changes {
		applied = append(applied, map[string]any{"path": c.Path, "rev": s.Entries[store][c.Path].Rev})
	}
	reply(w, 200, map[string]any{"commit": commit, "rev": s.Rev[store], "applied": applied, "conflicts": []any{}})
}

// apply records one commit of files and answers its number.
func (s *Server) apply(store, device, message string, files []Entry) int64 {
	commit := int64(len(s.Commits[store])) + 1
	s.Clock += 60_000
	var bytes int64
	for _, f := range files {
		s.Rev[store]++
		f.Rev, f.Device, f.Commit, f.Message, f.UpdatedAt = s.Rev[store], device, commit, message, s.Clock
		if f.Deleted {
			f.SHA256, f.Size = "", 0
		}
		bytes += f.Size
		s.Entries[store][f.Path] = f
	}
	s.Commits[store] = append(s.Commits[store], logEntry{Commit: commit, Message: message, Device: device, Files: len(files), Bytes: bytes, CreatedAt: s.Clock})
	return commit
}

// Put commits content as another device would.
func (s *Server) Put(store, path, content string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.PutLocked(store, path, content)
}

// PutLocked is Put for an OnCommit hook, which runs under the lock.
func (s *Server) PutLocked(store, path, content string) {
	sha := Hash([]byte(content))
	s.Blobs[sha] = []byte(content)
	s.apply(store, "other", "edit "+path, []Entry{{Path: path, SHA256: sha, Size: int64(len(content))}})
}

// Delete commits a deletion as another device would.
func (s *Server) Delete(store, path string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.apply(store, "other", "delete "+path, []Entry{{Path: path, Deleted: true}})
}

// Content is a path's current bytes on the server.
func (s *Server) Content(store, path string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.Entries[store][path]
	if !ok || e.Deleted {
		return "", false
	}
	return string(s.Blobs[e.SHA256]), true
}

// Lock guards direct reads and writes of the fields while requests run.
func (s *Server) Lock() func() {
	s.mu.Lock()
	return s.mu.Unlock
}
