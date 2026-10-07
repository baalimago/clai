package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// stubShellCommand shadows a command on PATH with a script, so tool tests
// assert the exact argv the tool builds without depending on the host's tools.
func stubShellCommand(t *testing.T, name, body string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatalf("write stub %s: %v", name, err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

const echoArgs = `printf '%s\n' "$@"`

func TestShellToolsRejectBadInputTypes(t *testing.T) {
	testCases := []struct {
		name  string
		call  func(pub_models.Input) (string, error)
		input pub_models.Input
	}{
		{name: "rg pattern", call: RipGrep.Call, input: pub_models.Input{"pattern": 1}},
		{name: "rg path", call: RipGrep.Call, input: pub_models.Input{"pattern": "x", "path": 1}},
		{name: "rg case_sensitive", call: RipGrep.Call, input: pub_models.Input{"pattern": "x", "case_sensitive": "yes"}},
		{name: "rg line_number", call: RipGrep.Call, input: pub_models.Input{"pattern": "x", "line_number": "yes"}},
		{name: "rg hidden", call: RipGrep.Call, input: pub_models.Input{"pattern": "x", "hidden": 1}},
		{name: "tree directory", call: FileTree.Call, input: pub_models.Input{}},
		{name: "tree level", call: FileTree.Call, input: pub_models.Input{"directory": "d", "level": "two"}},
		{name: "file file_path", call: FileType.Call, input: pub_models.Input{}},
		{name: "file mime_type", call: FileType.Call, input: pub_models.Input{"file_path": "f", "mime_type": "yes"}},
		{name: "ls directory", call: LS.Call, input: pub_models.Input{}},
		{name: "ls all", call: LS.Call, input: pub_models.Input{"directory": "d", "all": "yes"}},
		{name: "ls long", call: LS.Call, input: pub_models.Input{"directory": "d", "long": "yes"}},
		{name: "find directory", call: Find.Call, input: pub_models.Input{}},
		{name: "find name", call: Find.Call, input: pub_models.Input{"directory": "d", "name": 1}},
		{name: "find type", call: Find.Call, input: pub_models.Input{"directory": "d", "type": 1}},
		{name: "find maxdepth", call: Find.Call, input: pub_models.Input{"directory": "d", "maxdepth": "1"}},
		{name: "cat file", call: Cat.Call, input: pub_models.Input{}},
		{name: "cat number", call: Cat.Call, input: pub_models.Input{"file": "f", "number": 1}},
		{name: "cat showEnds", call: Cat.Call, input: pub_models.Input{"file": "f", "showEnds": 1}},
		{name: "cat squeezeBlank", call: Cat.Call, input: pub_models.Input{"file": "f", "squeezeBlank": 1}},
		{name: "go command", call: Go.Call, input: pub_models.Input{}},
		{name: "ffprobe file", call: FFProbe.Call, input: pub_models.Input{}},
		{name: "ffprobe format", call: FFProbe.Call, input: pub_models.Input{"file": "f", "format": 1}},
		{name: "ffprobe unsupported format", call: FFProbe.Call, input: pub_models.Input{"file": "f", "format": "yaml"}},
		{name: "ffprobe showFormat", call: FFProbe.Call, input: pub_models.Input{"file": "f", "showFormat": 1}},
		{name: "ffprobe showStreams", call: FFProbe.Call, input: pub_models.Input{"file": "f", "showStreams": 1}},
		{name: "ffprobe showFrames", call: FFProbe.Call, input: pub_models.Input{"file": "f", "showFrames": 1}},
		{name: "ffprobe selectStreams", call: FFProbe.Call, input: pub_models.Input{"file": "f", "selectStreams": 1}},
		{name: "ffprobe showEntries", call: FFProbe.Call, input: pub_models.Input{"file": "f", "showEntries": 1}},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := tc.call(tc.input); err == nil {
				t.Fatalf("%s accepted invalid input", tc.name)
			}
		})
	}
}

// assertArgs checks that every wanted token reached the shadowed command.
func assertArgs(t *testing.T, out string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(out, w) {
			t.Errorf("argv %q lacks %q", out, w)
		}
	}
}

func TestRipGrepCall(t *testing.T) {
	t.Run("flags reach the command", func(t *testing.T) {
		stubShellCommand(t, "rg", echoArgs)
		out, err := RipGrep.Call(pub_models.Input{
			"pattern": "needle", "path": "dir",
			"case_sensitive": true, "line_number": true, "hidden": true,
		})
		if err != nil {
			t.Fatalf("RipGrep: %v", err)
		}
		assertArgs(t, out, "needle", "dir", "--case-sensitive", "--line-number", "--hidden")
	})

	t.Run("exit status 1 means no hits, not an error", func(t *testing.T) {
		stubShellCommand(t, "rg", "exit 1")
		out, err := RipGrep.Call(pub_models.Input{"pattern": "none"})
		if err != nil {
			t.Fatalf("RipGrep: %v", err)
		}
		if !strings.Contains(out, "found no hits") {
			t.Fatalf("output = %q, want the no-hits message", out)
		}
	})

	t.Run("other failures are errors", func(t *testing.T) {
		stubShellCommand(t, "rg", "exit 2")
		if _, err := RipGrep.Call(pub_models.Input{"pattern": "x"}); err == nil {
			t.Fatal("RipGrep succeeded on a failing command")
		}
	})
}

func TestFileTreeCall(t *testing.T) {
	t.Run("level becomes a depth flag", func(t *testing.T) {
		stubShellCommand(t, "tree", echoArgs)
		out, err := FileTree.Call(pub_models.Input{"directory": "src", "level": float64(2)})
		if err != nil {
			t.Fatalf("FileTree: %v", err)
		}
		assertArgs(t, out, "src", "-L", "2")
	})

	t.Run("command failure is an error", func(t *testing.T) {
		stubShellCommand(t, "tree", "exit 1")
		if _, err := FileTree.Call(pub_models.Input{"directory": "src"}); err == nil {
			t.Fatal("FileTree succeeded on a failing command")
		}
	})
}

func TestFileTypeCall(t *testing.T) {
	t.Run("mime flag reaches the command", func(t *testing.T) {
		stubShellCommand(t, "file", echoArgs)
		out, err := FileType.Call(pub_models.Input{"file_path": "a.png", "mime_type": true})
		if err != nil {
			t.Fatalf("FileType: %v", err)
		}
		assertArgs(t, out, "a.png", "--mime-type")
	})

	t.Run("command failure is an error", func(t *testing.T) {
		stubShellCommand(t, "file", "exit 1")
		if _, err := FileType.Call(pub_models.Input{"file_path": "a.png"}); err == nil {
			t.Fatal("FileType succeeded on a failing command")
		}
	})
}

func TestLsCall(t *testing.T) {
	t.Run("all and long become flags", func(t *testing.T) {
		stubShellCommand(t, "ls", echoArgs)
		out, err := LS.Call(pub_models.Input{"directory": "here", "all": true, "long": true})
		if err != nil {
			t.Fatalf("Ls: %v", err)
		}
		assertArgs(t, out, "here", "-a", "-l")
	})

	t.Run("command failure is an error", func(t *testing.T) {
		stubShellCommand(t, "ls", "exit 1")
		if _, err := LS.Call(pub_models.Input{"directory": "here"}); err == nil {
			t.Fatal("Ls succeeded on a failing command")
		}
	})
}

func TestFindCall(t *testing.T) {
	t.Run("filters reach the command", func(t *testing.T) {
		stubShellCommand(t, "find", echoArgs)
		out, err := Find.Call(pub_models.Input{
			"directory": "root", "name": "*.go", "type": "f", "maxdepth": float64(3),
		})
		if err != nil {
			t.Fatalf("Find: %v", err)
		}
		assertArgs(t, out, "root", "-name", "*.go", "-type", "f", "-maxdepth", "3")
	})

	t.Run("command failure is an error", func(t *testing.T) {
		stubShellCommand(t, "find", "exit 1")
		if _, err := Find.Call(pub_models.Input{"directory": "root"}); err == nil {
			t.Fatal("Find succeeded on a failing command")
		}
	})
}

func TestCatCall(t *testing.T) {
	t.Run("formatting flags reach the command", func(t *testing.T) {
		stubShellCommand(t, "cat", echoArgs)
		out, err := Cat.Call(pub_models.Input{"file": "f.txt", "number": true, "showEnds": true, "squeezeBlank": true})
		if err != nil {
			t.Fatalf("Cat: %v", err)
		}
		assertArgs(t, out, "f.txt", "-n", "-E", "-s")
	})

	t.Run("command failure is an error", func(t *testing.T) {
		stubShellCommand(t, "cat", "exit 1")
		if _, err := Cat.Call(pub_models.Input{"file": "f.txt"}); err == nil {
			t.Fatal("Cat succeeded on a failing command")
		}
	})
}

func TestGoToolCall(t *testing.T) {
	t.Run("command, args and dir reach the go binary", func(t *testing.T) {
		stubShellCommand(t, "go", "pwd; "+echoArgs)
		workDir := t.TempDir()
		out, err := Go.Call(pub_models.Input{"command": "test", "args": "-run X ./...", "dir": workDir})
		if err != nil {
			t.Fatalf("Go: %v", err)
		}
		assertArgs(t, out, workDir, "test", "-run", "X", "./...")
	})

	t.Run("command failure is an error", func(t *testing.T) {
		stubShellCommand(t, "go", "exit 1")
		if _, err := Go.Call(pub_models.Input{"command": "build"}); err == nil {
			t.Fatal("Go succeeded on a failing command")
		}
	})
}

func TestFFProbeCall(t *testing.T) {
	t.Run("all options reach the command", func(t *testing.T) {
		stubShellCommand(t, "ffprobe", echoArgs)
		out, err := FFProbe.Call(pub_models.Input{
			"file": "clip.mp4", "format": "json",
			"showFormat": true, "showStreams": true, "showFrames": true,
			"selectStreams": "v:0", "showEntries": "format=duration",
		})
		if err != nil {
			t.Fatalf("FFProbe: %v", err)
		}
		assertArgs(t, out,
			"-hide_banner", "-of", "json", "-show_format", "-show_streams",
			"-show_frames", "-select_streams", "v:0", "-show_entries", "format=duration", "clip.mp4")
	})

	t.Run("default format adds no format flag", func(t *testing.T) {
		stubShellCommand(t, "ffprobe", echoArgs)
		out, err := FFProbe.Call(pub_models.Input{"file": "clip.mp4", "format": "default"})
		if err != nil {
			t.Fatalf("FFProbe: %v", err)
		}
		if strings.Contains(out, "-of") {
			t.Fatalf("argv %q has a format flag for the default format", out)
		}
	})

	t.Run("command failure is an error", func(t *testing.T) {
		stubShellCommand(t, "ffprobe", "exit 1")
		if _, err := FFProbe.Call(pub_models.Input{"file": "clip.mp4"}); err == nil {
			t.Fatal("FFProbe succeeded on a failing command")
		}
	})
}
