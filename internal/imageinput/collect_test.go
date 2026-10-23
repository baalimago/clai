package imageinput_test

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/baalimago/clai/internal/asset"
	"github.com/baalimago/clai/internal/glob"
	"github.com/baalimago/clai/internal/imageinput"
	"github.com/baalimago/clai/internal/utils"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// readmeImageLimit is the README parameter "Largest local image file read".
const readmeImageLimit = 20 << 20

const noPromptText = "found no prompt, set args or pipe in some string"

var (
	pngData  = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR png")
	jpegData = []byte("\xff\xd8\xff\xe0\x00\x10JFIF jpeg")
	gifData  = []byte("GIF89a gif")
	webpData = []byte("RIFF\x10\x00\x00\x00WEBPVP8 webp")
)

func forEachCollector(t *testing.T, run func(t *testing.T, c imageinput.ImageCollector)) {
	t.Helper()
	subjects := []struct {
		name string
		new  func() imageinput.ImageCollector
	}{
		{"New", imageinput.New},
	}
	for _, sub := range subjects {
		t.Run(sub.name, func(t *testing.T) { run(t, sub.new()) })
	}
}

func writeFile(t *testing.T, path string, data []byte) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

func userImage(data []byte, mime string) pub_models.ImagePart {
	return pub_models.ImagePart{MIME: mime, Origin: pub_models.OriginUser, Data: data}
}

func describe(images []pub_models.ImagePart) string {
	var b strings.Builder
	for _, im := range images {
		fmt.Fprintf(&b, "{origin=%s mime=%s asset=%q data=%q} ", im.Origin, im.MIME, im.Asset, im.Data)
	}
	return b.String()
}

func wantImages(t *testing.T, got []pub_models.ImagePart, want ...pub_models.ImagePart) {
	t.Helper()
	same := func(a, b pub_models.ImagePart) bool {
		return a.Asset == b.Asset && a.MIME == b.MIME && a.Origin == b.Origin && bytes.Equal(a.Data, b.Data)
	}
	if !slices.EqualFunc(got, want, same) {
		t.Fatalf("images = %s, want %s", describe(got), describe(want))
	}
}

func mustCollect(t *testing.T, c imageinput.ImageCollector, req imageinput.CollectRequest) imageinput.Collected {
	t.Helper()
	got, err := c.Collect(req)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	return got
}

func wantPrompt(t *testing.T, got imageinput.Collected, want string) {
	t.Helper()
	if got.Prompt != want {
		t.Fatalf("Prompt = %q, want %q", got.Prompt, want)
	}
}

// Proves Collect.1, AC1.
func TestCollect_ImgOrder(t *testing.T) {
	forEachCollector(t, func(t *testing.T, c imageinput.ImageCollector) {
		dir := t.TempDir()
		b := writeFile(t, filepath.Join(dir, "b.png"), jpegData)
		a := writeFile(t, filepath.Join(dir, "a.jpg"), pngData)
		writeFile(t, filepath.Join(dir, "shots", "2.png"), gifData)
		writeFile(t, filepath.Join(dir, "shots", "1.png"), webpData)

		got := mustCollect(t, c, imageinput.CollectRequest{
			Images: []string{b, a, filepath.Join(dir, "shots", "*.png")},
			Args:   []string{"q", "compare", "them"},
		})
		wantImages(t, got.Images,
			userImage(jpegData, "image/jpeg"),
			userImage(pngData, "image/png"),
			userImage(webpData, "image/webp"),
			userImage(gifData, "image/gif"),
		)
		wantPrompt(t, got, "compare them")
		if len(got.GlobMessages) != 0 {
			t.Fatalf("GlobMessages = %v, want none", got.GlobMessages)
		}

		t.Run("sources in order -img, -g, stdin", func(t *testing.T) {
			writeFile(t, filepath.Join(dir, "docs", "c.gif"), gifData)
			got := mustCollect(t, c, imageinput.CollectRequest{
				Images: []string{a},
				Glob:   filepath.Join(dir, "docs", "*"),
				Stdin:  bytes.NewReader(jpegData),
				Args:   []string{"q", "all"},
			})
			wantImages(t, got.Images,
				userImage(pngData, "image/png"),
				userImage(gifData, "image/gif"),
				userImage(jpegData, "image/jpeg"),
			)
		})
	})
}

// Proves Collect.1.
func TestCollect_Tilde(t *testing.T) {
	forEachCollector(t, func(t *testing.T, c imageinput.ImageCollector) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		writeFile(t, filepath.Join(home, "x.png"), pngData)
		writeFile(t, filepath.Join(home, "shots", "y.gif"), gifData)

		got := mustCollect(t, c, imageinput.CollectRequest{
			Images: []string{"~/x.png", "~/shots/*"},
			Args:   []string{"q", "hi"},
		})
		wantImages(t, got.Images, userImage(pngData, "image/png"), userImage(gifData, "image/gif"))
	})
}

// Proves Collect.6, AC2.
func TestCollect_GlobSplit(t *testing.T) {
	forEachCollector(t, func(t *testing.T, c imageinput.ImageCollector) {
		docs := filepath.Join(t.TempDir(), "docs")
		writeFile(t, filepath.Join(docs, "a.md"), []byte("# notes\nsee \"b\"\n"))
		writeFile(t, filepath.Join(docs, "b.png"), pngData)
		if err := os.Mkdir(filepath.Join(docs, "sub"), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		today, err := glob.CreateChat(filepath.Join(docs, "*.md"), "")
		if err != nil {
			t.Fatalf("today's glob path: %v", err)
		}
		want := today.Messages[1 : len(today.Messages)-1]
		if len(want) != 1 {
			t.Fatalf("today's glob path built %d file messages for a.md, want 1", len(want))
		}

		got := mustCollect(t, c, imageinput.CollectRequest{
			Glob: filepath.Join(docs, "*"),
			Args: []string{"q", "summarize"},
		})
		wantImages(t, got.Images, userImage(pngData, "image/png"))
		if !reflect.DeepEqual(got.GlobMessages, want) {
			t.Fatalf("GlobMessages = %#v, want %#v", got.GlobMessages, want)
		}
		wantPrompt(t, got, "summarize")
	})
}

// Proves Collect.6: a -g failure is today's glob error.
func TestCollect_GlobErrors(t *testing.T) {
	forEachCollector(t, func(t *testing.T, c imageinput.ImageCollector) {
		for _, tc := range []struct {
			name, glob string
			want       error
		}{
			{"no match", filepath.Join(t.TempDir(), "none", "*"), glob.ErrNoFiles},
			{"bad pattern", "[", filepath.ErrBadPattern},
		} {
			t.Run(tc.name, func(t *testing.T) {
				got, err := c.Collect(imageinput.CollectRequest{Glob: tc.glob, Args: []string{"q", "hi"}})
				if !errors.Is(err, tc.want) {
					t.Fatalf("Collect error = %v, want %v", err, tc.want)
				}
				if !strings.Contains(err.Error(), tc.glob) {
					t.Fatalf("error %q does not name the glob %q", err, tc.glob)
				}
				if got.Images != nil || got.GlobMessages != nil || got.Prompt != "" {
					t.Fatalf("Collected = %+v, want zero on error", got)
				}
			})
		}
	})
}

// Proves Collect.7, AC3.
func TestCollect_StdinRaw(t *testing.T) {
	forEachCollector(t, func(t *testing.T, c imageinput.ImageCollector) {
		got := mustCollect(t, c, imageinput.CollectRequest{
			Stdin: bytes.NewReader(pngData),
			Args:  []string{"q", "what", "breed"},
		})
		wantImages(t, got.Images, userImage(pngData, "image/png"))
		wantPrompt(t, got, "what breed")
		if len(got.GlobMessages) != 0 {
			t.Fatalf("GlobMessages = %v, want none", got.GlobMessages)
		}
	})
}

// Proves Collect.7, AC3.
func TestCollect_StdinBase64(t *testing.T) {
	forEachCollector(t, func(t *testing.T, c imageinput.ImageCollector) {
		for _, tc := range []struct {
			name, stdin string
			want        pub_models.ImagePart
		}{
			{"trailing newline", base64.StdEncoding.EncodeToString(pngData) + "\n", userImage(pngData, "image/png")},
			{"surrounding whitespace", " \t" + base64.StdEncoding.EncodeToString(jpegData) + "\r\n\n", userImage(jpegData, "image/jpeg")},
		} {
			t.Run(tc.name, func(t *testing.T) {
				got := mustCollect(t, c, imageinput.CollectRequest{
					Stdin: strings.NewReader(tc.stdin),
					Args:  []string{"q", "what", "breed"},
				})
				wantImages(t, got.Images, tc.want)
				wantPrompt(t, got, "what breed")
			})
		}
	})
}

// Proves Collect.8, I10, AC4. Each want is today's utils.Prompt output for the row.
func TestCollect_PromptParity(t *testing.T) {
	text := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("hello world, this is a text file\n", 20)))
	pngB64 := base64.StdEncoding.EncodeToString(append(slices.Clone(pngData), bytes.Repeat([]byte("x"), 100)...))
	wrapped := pngB64[:76] + "\n" + pngB64[76:] + "\n"
	in := func(s string) *string { return &s }
	rows := []struct {
		name    string
		args    []string
		replace string
		stdin   *string
		want    string
	}{
		{"utils: arguments only", []string{"cmd", "arg1", "arg2"}, "", nil, "arg1 arg2"},
		{"utils: stdin only", []string{"cmd"}, "", in("input from stdin"), "input from stdin"},
		{"utils: arguments and stdin", []string{"cmd", "arg1", "arg2", "{}"}, "{}", in("input from stdin"), "arg1 arg2 input from stdin"},
		{"utils: custom token", []string{"cmd", "prefix", "<stdin>", "suffix"}, "<stdin>", in("input from stdin"), "prefix input from stdin suffix"},
		{"utils: stdin appended", []string{"cmd", "prefix", "suffix"}, "", in("input from stdin"), "prefix suffix input from stdin"},
		{"utils: file redirect", []string{"cmd", "prefix"}, "", in("file content here"), "prefix file content here"},
		{"e2e: stdin_only_becomes_prompt", []string{"q"}, "", in("from-stdin"), "from-stdin"},
		{"e2e: stdin_replaces_default_token", []string{"q", "hello", "{}", "world"}, "", in("X"), "hello X world X"},
		{"e2e: stdin_replaces_custom_token", []string{"q", "hello", "__", "world"}, "", in("Y"), "hello __ world Y"},
		{"-I token", []string{"q", "hello", "__", "world"}, "__", in("Y"), "hello Y world"},
		{"empty stdin", []string{"q", "hi"}, "", in(""), "hi "},
		{"stdin with spaces", []string{"q"}, "", in("a b  c\n"), "a b  c\n"},
		{"text naming a format", []string{"q"}, "", in("PNG and GIF89a are formats"), "PNG and GIF89a are formats"},
		{"long base64 of a text file", []string{"q", "decode"}, "", in(text + "\n"), "decode " + text + "\n"},
		{"long base64 of a text file alone", []string{"q"}, "", in(text), text},
		{"line-wrapped base64 of a png", []string{"q", "look"}, "", in(wrapped), "look " + wrapped},
	}
	forEachCollector(t, func(t *testing.T, c imageinput.ImageCollector) {
		for _, row := range rows {
			t.Run(row.name, func(t *testing.T) {
				req := imageinput.CollectRequest{Args: slices.Clone(row.args), StdinReplace: row.replace}
				if row.stdin != nil {
					req.Stdin = strings.NewReader(*row.stdin)
				}
				got := mustCollect(t, c, req)
				wantPrompt(t, got, row.want)
				if len(got.Images) != 0 {
					t.Fatalf("Images = %s, want none", describe(got.Images))
				}
				if len(got.GlobMessages) != 0 {
					t.Fatalf("GlobMessages = %v, want none", got.GlobMessages)
				}
			})
		}
	})
}

// Proves Collect.8, and Collect.7's no-stdin rule for an image on stdin.
func TestCollect_NoPrompt(t *testing.T) {
	forEachCollector(t, func(t *testing.T, c imageinput.ImageCollector) {
		for _, tc := range []struct {
			name string
			req  imageinput.CollectRequest
		}{
			{"no args and nil stdin", imageinput.CollectRequest{Args: []string{"q"}}},
			{"no args and an image on stdin", imageinput.CollectRequest{Args: []string{"q"}, Stdin: bytes.NewReader(pngData)}},
			{"no args at all", imageinput.CollectRequest{}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				got, err := c.Collect(tc.req)
				if !errors.Is(err, utils.ErrNoPrompt) {
					t.Fatalf("Collect error = %v, want utils.ErrNoPrompt", err)
				}
				if err.Error() != noPromptText {
					t.Fatalf("error text = %q, want today's %q", err, noPromptText)
				}
				if got.Images != nil || got.Prompt != "" {
					t.Fatalf("Collected = %+v, want zero on error", got)
				}
			})
		}
	})
}

// Proves Collect.2–5, NoMatchError.1, AC5.
func TestCollect_Errors(t *testing.T) {
	forEachCollector(t, func(t *testing.T, c imageinput.ImageCollector) {
		dir := t.TempDir()
		good := writeFile(t, filepath.Join(dir, "good.png"), pngData)
		notes := writeFile(t, filepath.Join(dir, "notes.txt"), []byte("not an image"))
		locked := writeFile(t, filepath.Join(dir, "locked.png"), pngData)
		big := filepath.Join(dir, "big.png")
		f, err := os.Create(big)
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		if err := f.Truncate(readmeImageLimit + 1); err != nil {
			t.Fatalf("truncate: %v", err)
		}
		if err := f.Close(); err != nil {
			t.Fatalf("close: %v", err)
		}
		// Mode 000 on big proves the size check runs before any open.
		for _, p := range []string{locked, big} {
			if err := os.Chmod(p, 0o000); err != nil {
				t.Fatalf("chmod: %v", err)
			}
		}

		check := func(t *testing.T, images []string, verify func(t *testing.T, err error)) {
			t.Helper()
			got, err := c.Collect(imageinput.CollectRequest{Images: images, Args: []string{"q", "hi"}})
			if err == nil {
				t.Fatalf("Collect(%q) succeeded, want an error", images)
			}
			verify(t, err)
			if got.Images != nil || got.Prompt != "" {
				t.Fatalf("Collected = %+v, want zero on error", got)
			}
		}
		wantNoMatch := func(pattern string) func(t *testing.T, err error) {
			return func(t *testing.T, err error) {
				var nm *imageinput.NoMatchError
				if !errors.As(err, &nm) || nm.Pattern != pattern {
					t.Fatalf("error = %v, want *NoMatchError{%q}", err, pattern)
				}
				if !strings.Contains(nm.Error(), pattern) {
					t.Fatalf("NoMatchError %q does not name %q", nm, pattern)
				}
			}
		}
		wantNamed := func(target error, path string) func(t *testing.T, err error) {
			return func(t *testing.T, err error) {
				if !errors.Is(err, target) {
					t.Fatalf("error = %v, want %v", err, target)
				}
				if !strings.Contains(err.Error(), path) {
					t.Fatalf("error %q does not name %s", err, path)
				}
			}
		}

		t.Run("glob matches nothing", func(t *testing.T) {
			pattern := filepath.Join(dir, "nope", "*.png")
			check(t, []string{pattern}, wantNoMatch(pattern))
		})
		t.Run("path does not exist", func(t *testing.T) {
			pattern := filepath.Join(dir, "missing.png")
			check(t, []string{good, pattern}, wantNoMatch(pattern))
		})
		t.Run("bad pattern", func(t *testing.T) {
			check(t, []string{"["}, wantNamed(filepath.ErrBadPattern, "["))
		})
		t.Run("not an image", func(t *testing.T) {
			check(t, []string{good, notes}, wantNamed(asset.ErrNotImage, notes))
		})
		t.Run("too large", func(t *testing.T) {
			check(t, []string{big}, func(t *testing.T, err error) {
				var tl *asset.ImageTooLargeError
				if !errors.As(err, &tl) {
					t.Fatalf("error = %v, want *asset.ImageTooLargeError", err)
				}
				want := asset.ImageTooLargeError{Path: big, Size: readmeImageLimit + 1, Limit: readmeImageLimit}
				if *tl != want {
					t.Fatalf("ImageTooLargeError = %+v, want %+v", *tl, want)
				}
			})
		})
		t.Run("unreadable", func(t *testing.T) {
			check(t, []string{locked}, wantNamed(fs.ErrPermission, locked))
		})
		t.Run("directory", func(t *testing.T) {
			check(t, []string{dir}, wantNamed(fs.ErrInvalid, dir))
		})
	})
}

func TestCollect_StdinReadError(t *testing.T) {
	forEachCollector(t, func(t *testing.T, c imageinput.ImageCollector) {
		boom := errors.New("boom")
		_, err := c.Collect(imageinput.CollectRequest{Stdin: iotest.ErrReader(boom), Args: []string{"q", "hi"}})
		if !errors.Is(err, boom) {
			t.Fatalf("Collect error = %v, want the read error", err)
		}
	})
}

// Proves Collect.9, I6.
func TestCollect_NoStoreWrites(t *testing.T) {
	forEachCollector(t, func(t *testing.T, c imageinput.ImageCollector) {
		root := t.TempDir()
		cfg := filepath.Join(root, "config")
		if err := os.MkdirAll(cfg, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		t.Setenv("HOME", root)
		t.Setenv("CLAI_CONFIG_DIR", cfg)
		img := writeFile(t, filepath.Join(root, "in", "a.png"), pngData)
		writeFile(t, filepath.Join(root, "in", "docs", "b.gif"), gifData)
		writeFile(t, filepath.Join(root, "in", "docs", "c.txt"), []byte("text"))

		got := mustCollect(t, c, imageinput.CollectRequest{
			Images: []string{img},
			Glob:   filepath.Join(root, "in", "docs", "*"),
			Stdin:  strings.NewReader(base64.StdEncoding.EncodeToString(jpegData)),
			Args:   []string{"q", "hi"},
		})
		if len(got.Images) != 3 {
			t.Fatalf("Images = %s, want 3", describe(got.Images))
		}
		if _, err := os.Stat(filepath.Join(cfg, "conversations", "assets")); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("conversations/assets exists after Collect (stat err %v)", err)
		}
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() && d.Name() == "assets" {
				return fmt.Errorf("Collect created %s", path)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	})
}

// sparseFile writes head, then extends the file with zeros to size bytes.
func sparseFile(t *testing.T, path string, head []byte, size int64) string {
	t.Helper()
	writeFile(t, path, head)
	if err := os.Truncate(path, size); err != nil {
		t.Fatalf("truncate %s: %v", path, err)
	}
	return path
}

func wantTooLarge(t *testing.T, err error, path string, size int64) {
	t.Helper()
	var tl *asset.ImageTooLargeError
	if !errors.As(err, &tl) {
		t.Fatalf("error = %v, want *asset.ImageTooLargeError", err)
	}
	if want := (asset.ImageTooLargeError{Path: path, Size: size, Limit: readmeImageLimit}); *tl != want {
		t.Fatalf("ImageTooLargeError = %+v, want %+v", *tl, want)
	}
}

// Proves Collect.4 for -g image matches and image stdin (R1-10); text keeps today's rules (I10).
func TestCollect_OversizedImagesRefused(t *testing.T) {
	forEachCollector(t, func(t *testing.T, c imageinput.ImageCollector) {
		t.Run("-g image over the limit", func(t *testing.T) {
			dir := t.TempDir()
			writeFile(t, filepath.Join(dir, "a.md"), []byte("notes"))
			big := sparseFile(t, filepath.Join(dir, "big.png"), pngData, readmeImageLimit+1)
			got, err := c.Collect(imageinput.CollectRequest{Glob: filepath.Join(dir, "*"), Args: []string{"q", "compare"}})
			wantTooLarge(t, err, big, readmeImageLimit+1)
			if got.Images != nil || got.GlobMessages != nil || got.Prompt != "" {
				t.Fatalf("Collected = %+v, want zero on error", got)
			}
		})
		t.Run("-g image at the limit, text over it", func(t *testing.T) {
			dir := t.TempDir()
			edge := sparseFile(t, filepath.Join(dir, "edge.png"), pngData, readmeImageLimit)
			sparseFile(t, filepath.Join(dir, "log.txt"), []byte("log"), readmeImageLimit+1)
			got := mustCollect(t, c, imageinput.CollectRequest{Glob: filepath.Join(dir, "*"), Args: []string{"q", "compare"}})
			if len(got.Images) != 1 || int64(len(got.Images[0].Data)) != readmeImageLimit || got.Images[0].MIME != "image/png" {
				t.Fatalf("images = %d, want %s as one PNG of exactly the limit", len(got.Images), edge)
			}
			if len(got.GlobMessages) != 1 || !strings.Contains(got.GlobMessages[0].Content, "log.txt") {
				t.Fatalf("GlobMessages = %d, want log.txt as today's file message", len(got.GlobMessages))
			}
		})
		t.Run("stdin image over the limit", func(t *testing.T) {
			data := make([]byte, readmeImageLimit+1)
			copy(data, pngData)
			got, err := c.Collect(imageinput.CollectRequest{Stdin: bytes.NewReader(data), Args: []string{"q", "what"}})
			wantTooLarge(t, err, "stdin", readmeImageLimit+1)
			if got.Images != nil || got.Prompt != "" {
				t.Fatalf("Collected = %+v, want zero on error", got)
			}
		})
	})
}
