package remote

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

const remoteStrongList = `["claude-opus-4-7","gpt-5.4","gemini-3.1-pro-preview","claude-opus-4-7"]`

func TestParseModelListTarget(t *testing.T) {
	for _, tc := range []struct{ in, want, fetch string }{
		{"cchub:ada/strong@2", "cchub:ada/strong@2", "/hub/ada/strong/v/2/raw"},
		{"gh:ada/models", "gh:ada/models/models.json", "https://raw.githubusercontent.com/ada/models/HEAD/models.json"},
		{"gh:ada/models/tiers@v1", "gh:ada/models/tiers/models.json@v1", "https://raw.githubusercontent.com/ada/models/v1/tiers/models.json"},
		{"gh:ada/models/fast.json@v1", "gh:ada/models/fast.json@v1", "https://raw.githubusercontent.com/ada/models/v1/fast.json"},
		{"https://example.com/models.json", "https://example.com/models.json", "https://example.com/models.json"},
	} {
		got, err := ParseModelListTarget(tc.in)
		if err != nil {
			t.Fatalf("ParseModelListTarget(%q): %v", tc.in, err)
		}
		if got.String() != tc.want || !strings.HasSuffix(got.FetchURL("https://hub"), tc.fetch) {
			t.Fatalf("ParseModelListTarget(%q) = %s fetching %s, want %s fetching %s", tc.in, got, got.FetchURL("https://hub"), tc.want, tc.fetch)
		}
	}
	if _, err := ParseModelListTarget("strong"); !errors.Is(err, ErrNotRemote) {
		t.Fatalf("a bare name: err = %v, want ErrNotRemote", err)
	}
}

func TestValidateModelList_deduplicatesPreservingOrder(t *testing.T) {
	got, err := ValidateModelList("cchub:ada/strong@1", []byte(`[" claude-opus-4-7 ", "gpt-5.4", "claude-opus-4-7", "gemini-3.1-pro-preview", "gpt-5.4"]`))
	if err != nil {
		t.Fatalf("ValidateModelList: %v", err)
	}
	want := []string{"claude-opus-4-7", "gpt-5.4", "gemini-3.1-pro-preview"}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestValidateModelList_rejectsInvalidBodies(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want string
	}{
		{"object", `{"models":["gpt-5.4"]}`, "not a JSON array of model names"},
		{"null", `null`, "not a JSON array of model names"},
		{"trailing", `["gpt-5.4"] ["claude-opus-4-7"]`, "more follows the first"},
		{"empty", `[]`, "lists no models"},
		{"blank", `["gpt-5.4", "   "]`, "model[1]: is blank"},
		{"nested remote", `["gpt-5.4", "cchub:ada/other"]`, "is a remote target"},
		{"control char", "[\"gpt-5.4\", \"bad\\u001bmodel\"]", "holds control characters"},
		{"oversize", `["` + strings.Repeat("a", MaxBodyBytes) + `"]`, "larger than 64 KiB"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ValidateModelList("cchub:ada/bad@1", []byte(tc.body))
			var invalid *InvalidModelListError
			if !errors.As(err, &invalid) || invalid.ExitCode() != ExitRefused {
				t.Fatalf("err = %v, want *InvalidModelListError with ExitRefused", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q lacks %q", err.Error(), tc.want)
			}
		})
	}
}

func TestResolveModelList_fetchesCachesAndNeedsNoTrustPrompt(t *testing.T) {
	h := newHub(t, map[string]string{"/hub/ada/strong/v/1/raw": remoteStrongList})
	var asked int
	r := testResolver(t, h, func(_ context.Context, _ TrustPrompt) (bool, error) {
		asked++
		return true, nil
	})
	// Prompt is nil to prove ResolveModelList never requires a trust prompt or trust.json entry.
	r.Prompt = nil

	got, err := r.ResolveModelList(t.Context(), "cchub:ada/strong@1")
	if err != nil {
		t.Fatalf("ResolveModelList: %v", err)
	}
	want := []string{"claude-opus-4-7", "gpt-5.4", "gemini-3.1-pro-preview"}
	if !got.Fetched || !slices.Equal(got.Models, want) || asked != 0 {
		t.Fatalf("got = %+v, asked = %d", got, asked)
	}

	// Second call on a pinned target serves from cache without hitting the hub.
	again, err := r.ResolveModelList(t.Context(), "cchub:ada/strong@1")
	if err != nil || again.Fetched || h.hits.Load() != 1 || !slices.Equal(again.Models, want) {
		t.Fatalf("second ResolveModelList: %+v, err=%v, hits=%d", again, err, h.hits.Load())
	}

	// Offline mode serves cached targets and refuses uncached ones with OfflineError.
	r.Offline = true
	offlineGot, err := r.ResolveModelList(t.Context(), "cchub:ada/strong@1")
	if err != nil || offlineGot.Fetched || !slices.Equal(offlineGot.Models, want) {
		t.Fatalf("offline cached: %+v, %v", offlineGot, err)
	}
	var offlineErr *OfflineError
	if _, err := r.ResolveModelList(t.Context(), "cchub:ada/missing@1"); !errors.As(err, &offlineErr) || offlineErr.ExitCode() != ExitRefused {
		t.Fatalf("offline uncached: err = %v, want *OfflineError", err)
	}
}

func TestResolveModelList_verifiesOptionalSHA256(t *testing.T) {
	h := newHub(t, map[string]string{"/hub/ada/strong/v/1/raw": remoteStrongList})
	r := testResolver(t, h, nil)
	digest := Digest([]byte(remoteStrongList))

	if _, err := r.ResolveModelList(t.Context(), "cchub:ada/strong@1#sha256="+digest); err != nil {
		t.Fatalf("matching sha256: %v", err)
	}
	var mismatch *HashMismatchError
	if _, err := r.ResolveModelList(t.Context(), "cchub:ada/strong@1#sha256="+strings.Repeat("0", 64)); !errors.As(err, &mismatch) || mismatch.ExitCode() != ExitRefused {
		t.Fatalf("wrong sha256: err = %v, want *HashMismatchError", err)
	}
}

func TestValidate_checksRemoteModelTargetInProfile(t *testing.T) {
	if _, err := Validate("x", []byte(`{"model":"cchub:ada/strong@1","prompt":"hi"}`)); err != nil {
		t.Fatalf("valid remote model target: %v", err)
	}
	_, err := Validate("x", []byte(`{"model":"http://insecure.example.com/models.json","prompt":"hi"}`))
	if err == nil || !strings.Contains(err.Error(), "only https:// is fetched") {
		t.Fatalf("insecure remote model target: err = %v", err)
	}
}

func TestResolveModelTarget_andErrorPaths(t *testing.T) {
	h := newHub(t, map[string]string{
		"/hub/ada/strong/v/1/raw": remoteStrongList,
		"/hub/ada/bad/v/1/raw":    `{}`,
	})
	newRes := func(confDir string, prompt Prompter) (Resolver, error) {
		return testResolver(t, h, prompt), nil
	}
	models, err := ResolveModelTarget(t.Context(), t.TempDir(), newRes, "cchub:ada/strong@1")
	if err != nil || len(models) != 3 {
		t.Fatalf("ResolveModelTarget: %v, %v", models, err)
	}
	if _, err := ResolveModelTarget(t.Context(), t.TempDir(), newRes, "cchub:ada/bad@1"); err == nil {
		t.Fatal("invalid model list body: want error")
	}
	if _, err := ResolveModelTarget(t.Context(), t.TempDir(), newRes, "cchub:Bad"); err == nil {
		t.Fatal("invalid target syntax: want error")
	}
	failRes := func(string, Prompter) (Resolver, error) {
		return Resolver{}, errors.New("resolver boom")
	}
	if _, err := ResolveModelTarget(t.Context(), t.TempDir(), failRes, "cchub:ada/strong@1"); err == nil || !strings.Contains(err.Error(), "resolver boom") {
		t.Fatalf("resolver error: %v", err)
	}
}
