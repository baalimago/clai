package present_test

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/png"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	imgasset "github.com/baalimago/clai/internal/asset"
	"github.com/baalimago/clai/internal/present"
	"github.com/baalimago/clai/internal/utils"
)

const (
	ansiReset = "\x1b[0m"
	home      = "/home/u"
	handle    = "3f9a2c"
	asset     = home + "/.config/clai/conversations/assets/3f9a2c0d.png"
)

var escapes = regexp.MustCompile(`\x1b\][^\x1b\a]*(?:\x1b\\|\a)|\x1b\[[0-9;]*m`)

type fakeOpener struct {
	err   error
	out   *bytes.Buffer
	calls []string
	// seen is Out's content at each call, to prove the line precedes the open.
	seen []string
}

func (f *fakeOpener) Open(path string) error {
	f.calls = append(f.calls, path)
	if f.out != nil {
		f.seen = append(f.seen, f.out.String())
	}
	return f.err
}

type failingWriter struct{ err error }

func (w failingWriter) Write([]byte) (int, error) { return 0, w.err }

func forEachPresenter(t *testing.T, run func(t *testing.T, newPresenter func(present.Options) present.Presenter)) {
	t.Helper()
	subjects := []struct {
		name string
		new  func(present.Options) present.Presenter
	}{
		{"New", present.New},
	}
	for _, sub := range subjects {
		t.Run(sub.name, func(t *testing.T) { run(t, sub.new) })
	}
}

func terminalOptions(out *bytes.Buffer, opener present.Opener) present.Options {
	return present.Options{Out: out, Terminal: true, Color: true, Opener: opener}
}

// Proves Present.1, AC1.
func TestPresent_TerminalLine(t *testing.T) {
	forEachPresenter(t, func(t *testing.T, newPresenter func(present.Options) present.Presenter) {
		for _, tc := range []struct {
			name, path, url string
		}{
			{"under home", asset, "file://" + asset},
			{"outside home", "/tmp/out/x.png", "file:///tmp/out/x.png"},
			{"space in path", home + "/my pics/x.png", "file:///home/u/my%20pics/x.png"},
			{"percent and hash", "/tmp/a%b#c.png", "file:///tmp/a%25b%23c.png"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				var out bytes.Buffer
				opts := terminalOptions(&out, &fakeOpener{})
				if err := newPresenter(opts).Present(present.Shown{Handle: handle, Path: tc.path}); err != nil {
					t.Fatalf("Present: %v", err)
				}
				got := out.String()
				for _, want := range []string{
					"\x1b]8;;" + tc.url + "\x1b\\",
					"\x1b]8;;\x1b\\",
					utils.RoleColor("tool") + tc.url,
					utils.TableTheme().Primary + "▸" + ansiReset,
				} {
					if !strings.Contains(got, want) {
						t.Errorf("line %q lacks %q", got, want)
					}
				}
				if plain, want := escapes.ReplaceAllString(got, ""), "▸ image "+handle+" → "+tc.url+"\n"; plain != want {
					t.Errorf("visible text = %q, want %q", plain, want)
				}
			})
		}
	})
}

// Proves Present.2, AC2.
func TestPresent_PipedBarePath(t *testing.T) {
	forEachPresenter(t, func(t *testing.T, newPresenter func(present.Options) present.Presenter) {
		for _, tc := range []struct {
			name            string
			terminal, color bool
		}{
			{"piped", false, true},
			{"piped without colour", false, false},
			{"terminal without colour", true, false},
		} {
			t.Run(tc.name, func(t *testing.T) {
				var out bytes.Buffer
				opts := present.Options{Out: &out, Terminal: tc.terminal, Color: tc.color, Opener: &fakeOpener{}}
				if err := newPresenter(opts).Present(present.Shown{Handle: handle, Path: asset}); err != nil {
					t.Fatalf("Present: %v", err)
				}
				if got := out.String(); got != asset+"\n" {
					t.Fatalf("output = %q, want %q", got, asset+"\n")
				}
			})
		}
	})
}

// Proves Present.3, U2, AC3.
func TestPresent_OpenGate(t *testing.T) {
	forEachPresenter(t, func(t *testing.T, newPresenter func(present.Options) present.Presenter) {
		for _, autoOpen := range []bool{false, true} {
			for _, terminal := range []bool{false, true} {
				for _, color := range []bool{false, true} {
					t.Run(fmt.Sprintf("auto=%v/terminal=%v/color=%v", autoOpen, terminal, color), func(t *testing.T) {
						var out bytes.Buffer
						opener := &fakeOpener{out: &out}
						opts := present.Options{Out: &out, Terminal: terminal, Color: color, AutoOpen: autoOpen, Opener: opener}
						if err := newPresenter(opts).Present(present.Shown{Handle: handle, Path: asset}); err != nil {
							t.Fatalf("Present: %v", err)
						}
						want := 0
						if autoOpen && terminal {
							want = 1
						}
						if len(opener.calls) != want {
							t.Fatalf("Open called %d times, want %d", len(opener.calls), want)
						}
						if want == 1 && opener.calls[0] != asset {
							t.Fatalf("Open(%q), want Open(%q)", opener.calls[0], asset)
						}
						if want == 1 && opener.seen[0] != out.String() {
							t.Fatalf("Open ran before the line was written: Out held %q", opener.seen[0])
						}
					})
				}
			}
		}
	})
}

// Proves Present.4, U2, I7, AC4.
func TestPresent_OpenFailsNonFatal(t *testing.T) {
	boom := errors.New("viewer crashed")
	inner := &present.OpenFailedError{Path: asset, Cause: boom}
	unavailable := &present.OpenerUnavailableError{Command: "feh"}
	forEachPresenter(t, func(t *testing.T, newPresenter func(present.Options) present.Presenter) {
		for _, tc := range []struct {
			name   string
			opener present.Opener
			color  bool
			cause  error
		}{
			{"plain error", &fakeOpener{err: boom}, true, boom},
			{"plain error without colour", &fakeOpener{err: boom}, false, boom},
			{"opener's own OpenFailedError", &fakeOpener{err: inner}, true, boom},
			{"opener unavailable", &fakeOpener{err: unavailable}, true, unavailable},
			{"no opener", nil, true, nil},
		} {
			t.Run(tc.name, func(t *testing.T) {
				var out bytes.Buffer
				opts := present.Options{Out: &out, Terminal: true, Color: tc.color, AutoOpen: true, Opener: tc.opener}
				err := newPresenter(opts).Present(present.Shown{Handle: handle, Path: asset})
				var failed *present.OpenFailedError
				if !errors.As(err, &failed) {
					t.Fatalf("Present error = %v (%T), want *OpenFailedError", err, err)
				}
				if failed.Path != asset {
					t.Fatalf("OpenFailedError.Path = %q, want %q", failed.Path, asset)
				}
				if failed.Cause == nil {
					t.Fatal("OpenFailedError.Cause is nil")
				}
				if tc.cause != nil && !errors.Is(err, tc.cause) {
					t.Fatalf("error %v does not wrap %v", err, tc.cause)
				}
				if strings.Count(err.Error(), asset) != 1 {
					t.Fatalf("error %q should name the path once", err)
				}
				want := asset + "\n"
				if tc.color {
					want = "▸ image " + handle + " → file://" + asset + "\n"
				}
				if got := escapes.ReplaceAllString(out.String(), ""); got != want {
					t.Fatalf("line after a failed open = %q, want %q", got, want)
				}
			})
		}
	})
}

// Proves Present.5.
func TestPresent_WriteFails(t *testing.T) {
	writeErr := errors.New("disk full")
	forEachPresenter(t, func(t *testing.T, newPresenter func(present.Options) present.Presenter) {
		for _, terminal := range []bool{false, true} {
			t.Run(fmt.Sprintf("terminal=%v", terminal), func(t *testing.T) {
				opener := &fakeOpener{}
				opts := present.Options{Out: failingWriter{writeErr}, Terminal: terminal, Color: true, AutoOpen: true, Opener: opener}
				err := newPresenter(opts).Present(present.Shown{Handle: handle, Path: asset})
				if !errors.Is(err, writeErr) {
					t.Fatalf("Present error = %v, want the write error", err)
				}
				var failed *present.OpenFailedError
				if errors.As(err, &failed) {
					t.Fatalf("a write failure must not be an OpenFailedError: %v", err)
				}
				if len(opener.calls) != 0 {
					t.Fatalf("Open called %d times after a failed write", len(opener.calls))
				}
			})
		}
		t.Run("nil Out", func(t *testing.T) {
			opener := &fakeOpener{}
			opts := present.Options{Terminal: true, AutoOpen: true, Opener: opener}
			if err := newPresenter(opts).Present(present.Shown{Handle: handle, Path: asset}); err == nil {
				t.Fatal("Present with no Out succeeded")
			}
			if len(opener.calls) != 0 {
				t.Fatalf("Open called %d times with no Out", len(opener.calls))
			}
		})
	})
}

func TestErrorMessages(t *testing.T) {
	cause := errors.New("exit status 3")
	failed := &present.OpenFailedError{Path: asset, Cause: cause}
	if msg := failed.Error(); !strings.Contains(msg, asset) || !strings.Contains(msg, "exit status 3") {
		t.Fatalf("OpenFailedError.Error() = %q, want path and cause", msg)
	}
	if !errors.Is(failed, cause) || failed.Unwrap() != cause {
		t.Fatal("OpenFailedError does not unwrap to its Cause")
	}
	if msg := (&present.OpenerUnavailableError{Command: "feh"}).Error(); !strings.Contains(msg, `"feh"`) {
		t.Fatalf("OpenerUnavailableError.Error() = %q, want the command", msg)
	}
	if msg := (&present.OpenerUnavailableError{}).Error(); !strings.Contains(msg, "image-opener") {
		t.Fatalf("OpenerUnavailableError{}.Error() = %q, want a hint at the image-opener setting", msg)
	}
}

// Proves Present.1–2 with a rebus: shown in brackets after the marker on a colour
// terminal, never in piped output.
func TestPresent_Rebus(t *testing.T) {
	img := present.Shown{Handle: handle, Path: asset, Rebus: "🐕 🎾 🎨"}
	var out bytes.Buffer
	if err := present.New(terminalOptions(&out, &fakeOpener{})).Present(img); err != nil {
		t.Fatalf("Present: %v", err)
	}
	if got, want := escapes.ReplaceAllString(out.String(), ""), "▸ [🐕 🎾 🎨] image "+handle+" → file://"+asset+"\n"; got != want {
		t.Fatalf("visible text = %q, want %q", got, want)
	}
	out.Reset()
	if err := present.New(present.Options{Out: &out, Opener: &fakeOpener{}}).Present(img); err != nil {
		t.Fatalf("Present: %v", err)
	}
	if out.String() != asset+"\n" {
		t.Fatalf("piped line = %q, want the bare path", out.String())
	}
}

// Proves Present.1 with details: model, size with ratio, file size and time, in that
// order, each left out when zero; piped output stays the bare path.
func TestPresent_Details(t *testing.T) {
	for _, tc := range []struct {
		name    string
		details present.Details
		want    string
	}{
		{"all", present.Details{Model: "gpt-image-1", Size: imgasset.Size{Width: 1536, Height: 1024}, Bytes: 1468006, Elapsed: 41*time.Second + 300*time.Millisecond}, " · gpt-image-1 · 1536×1024 (3:2) · 1.5 MB · 41s"},
		{"square kilobytes", present.Details{Size: imgasset.Size{Width: 1024, Height: 1024}, Bytes: 524288}, " · 1024×1024 (1:1) · 524 KB"},
		{"odd ratio", present.Details{Size: imgasset.Size{Width: 1365, Height: 768}}, " · 1365×768 (1.78:1)"},
		{"ratio terms at the limit", present.Details{Size: imgasset.Size{Width: 32, Height: 31}}, " · 32×31 (32:31)"},
		{"ratio terms past the limit", present.Details{Size: imgasset.Size{Width: 33, Height: 32}}, " · 33×32 (1.03:1)"},
		{"half a size", present.Details{Size: imgasset.Size{Width: 32, Height: 0}}, ""},
		{"bytes and sub-second", present.Details{Bytes: 83, Elapsed: 120*time.Millisecond + 400*time.Microsecond}, " · 83 B · 120ms"},
		{"under a millisecond", present.Details{Elapsed: 300 * time.Microsecond}, " · <1ms"},
		{"a millisecond", present.Details{Elapsed: time.Millisecond}, " · 1ms"},
		{"rounds up to the millisecond", present.Details{Elapsed: 120*time.Millisecond + 600*time.Microsecond}, " · 121ms"},
		{"rounds up to the second", present.Details{Elapsed: 1600 * time.Millisecond}, " · 2s"},
		{"999 bytes", present.Details{Bytes: 999}, " · 999 B"},
		{"1000 bytes", present.Details{Bytes: 1000}, " · 1 KB"},
		{"just under a megabyte", present.Details{Bytes: 999_499}, " · 999 KB"},
		{"rounds to a megabyte", present.Details{Bytes: 999_500}, " · 1.0 MB"},
		{"none", present.Details{}, ""},
	} {
		var out bytes.Buffer
		img := present.Shown{Handle: handle, Path: asset, Details: tc.details}
		if err := present.New(terminalOptions(&out, &fakeOpener{})).Present(img); err != nil {
			t.Fatalf("%s: Present: %v", tc.name, err)
		}
		if got, want := escapes.ReplaceAllString(out.String(), ""), "▸ image "+handle+tc.want+" → file://"+asset+"\n"; got != want {
			t.Errorf("%s: visible text = %q, want %q", tc.name, got, want)
		}
		if tc.want != "" && !strings.Contains(out.String(), utils.TableTheme().Secondary+tc.want+"\x1b[0m") {
			t.Errorf("%s: details are not in the secondary colour: %q", tc.name, out.String())
		}
		out.Reset()
		if err := present.New(present.Options{Out: &out, Opener: &fakeOpener{}}).Present(img); err != nil || out.String() != asset+"\n" {
			t.Errorf("%s: piped line = %q, %v; want the bare path", tc.name, out.String(), err)
		}
	}
}

// Proves DetailsOf.1: the size and byte count come from the image bytes.
func TestDetailsOf(t *testing.T) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewGray(image.Rect(0, 0, 30, 20))); err != nil {
		t.Fatal(err)
	}
	got := present.DetailsOf(buf.Bytes(), "m", time.Second)
	want := present.Details{Model: "m", Size: imgasset.Size{Width: 30, Height: 20}, Bytes: buf.Len(), Elapsed: time.Second}
	if got != want {
		t.Fatalf("DetailsOf = %+v, want %+v", got, want)
	}
	if got := present.DetailsOf([]byte("webp?"), "m", 0); got.Size != (imgasset.Size{}) || got.Bytes != 5 {
		t.Fatalf("DetailsOf(unmeasurable) = %+v, want no size and 5 bytes", got)
	}
}

// Proves Rows.1–2: the line as two rows, coloured and linked like Present.1 with
// Color, plain text without it.
func TestPresenter_Rows(t *testing.T) {
	img := present.Shown{Handle: handle, Path: home + "/my pics/x.png", Rebus: "🐕", Details: present.Details{Model: "m", Bytes: 83}}
	link := "file:///home/u/my%20pics/x.png"
	plain := present.New(present.Options{Terminal: true}).Rows(img)
	if want := []string{"▸ [🐕] image " + handle + " · m · 83 B", "→ " + link}; !slices.Equal(plain, want) {
		t.Fatalf("plain Rows = %q, want %q", plain, want)
	}
	coloured := present.New(present.Options{Terminal: true, Color: true}).Rows(img)
	if len(coloured) != 2 {
		t.Fatalf("coloured Rows = %q, want two rows", coloured)
	}
	for i, want := range []string{plain[0], plain[1]} {
		if got := escapes.ReplaceAllString(coloured[i], ""); got != want {
			t.Errorf("coloured row %d reads %q, want %q", i, got, want)
		}
	}
	for _, want := range []string{utils.TableTheme().Primary + "▸", utils.TableTheme().Secondary + " · m · 83 B"} {
		if !strings.Contains(coloured[0], want) {
			t.Errorf("row 0 %q lacks %q", coloured[0], want)
		}
	}
	if !strings.Contains(coloured[1], "\x1b]8;;"+link+"\x1b\\") || !strings.Contains(coloured[1], utils.RoleColor("tool")+link) {
		t.Errorf("row 1 %q is not the coloured OSC 8 link", coloured[1])
	}
	bare := present.New(present.Options{Terminal: true, Color: true}).Rows(present.Shown{Handle: handle, Path: asset})
	if regexp.MustCompile(`\x1b\[[0-9;]*m\x1b\[0m`).MatchString(bare[0]) {
		t.Errorf("row 0 %q carries an empty colour pair", bare[0])
	}
}

// Proves Open.1–2: it opens like Present, and never writes.
func TestPresenter_Open(t *testing.T) {
	for _, tc := range []struct {
		autoOpen, terminal bool
		opens              int
	}{{true, true, 1}, {true, false, 0}, {false, true, 0}} {
		var out bytes.Buffer
		opener := &fakeOpener{}
		p := present.New(present.Options{Out: &out, Terminal: tc.terminal, Color: true, AutoOpen: tc.autoOpen, Opener: opener})
		if err := p.Open(present.Shown{Handle: handle, Path: asset}); err != nil || len(opener.calls) != tc.opens || out.Len() != 0 {
			t.Errorf("AutoOpen %v, Terminal %v: err %v, opens %d, wrote %q; want %d opens and no output", tc.autoOpen, tc.terminal, err, len(opener.calls), out.String(), tc.opens)
		}
	}
	failing := present.New(present.Options{Out: &bytes.Buffer{}, Terminal: true, AutoOpen: true, Opener: &fakeOpener{err: errors.New("no viewer")}})
	var failed *present.OpenFailedError
	if err := failing.Open(present.Shown{Handle: handle, Path: asset}); !errors.As(err, &failed) || failed.Path != asset {
		t.Fatalf("Open = %v, want *OpenFailedError for %s", err, asset)
	}
}
