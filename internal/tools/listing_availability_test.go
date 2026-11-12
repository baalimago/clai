package tools

import (
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/baalimago/clai/internal/testenv/procenv"
)

// withListingUniverse fills a fresh registry through the real registration
// path, resolving exactly the executables in found, so a listing test decides
// which tools this machine can load.
func withListingUniverse(t *testing.T, found []string, fn func()) {
	t.Helper()
	t.Setenv("CLAI_CONFIG_DIR", filepath.Join(t.TempDir(), "absent"))
	t.Setenv("CLAI_CACHE_DIR", t.TempDir())
	WithTestRegistry(t, func() {
		registerLocalTools(Registry, func(name string) (string, error) {
			if slices.Contains(found, name) {
				return "/bin/" + name, nil
			}
			return "", exec.ErrNotFound
		})
		fn()
	})
}

// everyProgram names each executable the catalog depends on.
func everyProgram() []string {
	var found []string
	for _, b := range builtins() {
		if b.executable != "" && !slices.Contains(found, b.executable) {
			found = append(found, b.executable)
		}
	}
	return found
}

// visibleBuiltinNames is the catalog as the listing shows it: hidden tools stay
// out.
func visibleBuiltinNames() []string {
	var names []string
	for _, b := range builtins() {
		if b.hidden {
			continue
		}
		names = append(names, b.tool.Specification().Name)
	}
	slices.Sort(names)
	return names
}

func listingJSON(t *testing.T) []toolJSON {
	t.Helper()
	ctx, stdout := jsonListingContext(t)
	if err := List(ctx); err != nil {
		t.Fatalf("List: %v", err)
	}
	var got []toolJSON
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal the listing: %v\n%s", err, stdout.String())
	}
	names := make([]string, 0, len(got))
	for _, entry := range got {
		names = append(names, entry.Name)
	}
	if !slices.IsSorted(names) {
		t.Fatalf("the listing is not in name order, so two machines cannot compare it: %v", names)
	}
	return got
}

func listingText(t *testing.T) string {
	t.Helper()
	return captureSubCmdOut(t, func() {
		if err := List(procenv.Context(context.Background())); err != nil {
			t.Fatalf("List: %v", err)
		}
	})
}

// TestListingNamesEveryBuiltinWithItsDependencyAndAvailability pins the point
// of the catalog: the list is clai's own, not the machine's toolchain, and each
// entry says what it needs and whether this machine has that program.
func TestListingNamesEveryBuiltinWithItsDependencyAndAvailability(t *testing.T) {
	cases := []struct {
		name  string
		found []string
	}{
		{"nothing found", nil},
		{"everything found", everyProgram()},
		{"one program found", []string{"jq"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withListingUniverse(t, tc.found, func() {
				listed := listingJSON(t)
				byName := make(map[string]toolJSON, len(listed))
				var names []string
				for _, entry := range listed {
					byName[entry.Name] = entry
					names = append(names, entry.Name)
				}
				slices.Sort(names)
				if want := visibleBuiltinNames(); !slices.Equal(names, want) {
					t.Fatalf("listed %v, want the whole catalog %v", names, want)
				}
				for _, b := range builtins() {
					if b.hidden {
						continue
					}
					entry := byName[b.tool.Specification().Name]
					if entry.Dependency != b.executable {
						t.Fatalf("%s dependency = %q, want %q", entry.Name, entry.Dependency, b.executable)
					}
					wantAvailable := b.executable == "" || slices.Contains(tc.found, b.executable)
					if entry.Available != wantAvailable {
						t.Fatalf("%s available = %v, want %v", entry.Name, entry.Available, wantAvailable)
					}
					if entry.Description == "" {
						t.Fatalf("%s is listed without its description", entry.Name)
					}
				}
			})
		})
	}
}

// TestListingTextMarksToolsThisMachineCannotLoad pins the single text mark and
// the footer that explains it: a marked tool is still listed, and an unmarked
// one needs no explanation.
func TestListingTextMarksToolsThisMachineCannotLoad(t *testing.T) {
	t.Run("nothing found marks every executable tool", func(t *testing.T) {
		withListingUniverse(t, nil, func() {
			got := listingText(t)
			if !strings.HasPrefix(got, "Available Tools:\n") {
				t.Fatalf("expected the listing header, got:\n%s", got)
			}
			for _, b := range builtins() {
				if b.hidden || b.executable == "" {
					continue
				}
				want := "- " + b.tool.Specification().Name + " [needs " + b.executable + "]"
				if !strings.Contains(got, want) {
					t.Fatalf("expected %q in the listing, got:\n%s", want, got)
				}
			}
			if !strings.Contains(got, "Note: tools marked [needs <program>]") {
				t.Fatalf("expected the mark explained in the footer, got:\n%s", got)
			}
		})
	})

	t.Run("everything found marks nothing", func(t *testing.T) {
		withListingUniverse(t, everyProgram(), func() {
			got := listingText(t)
			if strings.Contains(got, "[needs ") {
				t.Fatalf("expected no mark on a machine that has every program, got:\n%s", got)
			}
			if strings.Contains(got, "tools marked") {
				t.Fatalf("expected no explanation with nothing marked, got:\n%s", got)
			}
		})
	})

	t.Run("one program found marks only its dependents", func(t *testing.T) {
		withListingUniverse(t, []string{"jq"}, func() {
			got := listingText(t)
			if !strings.Contains(got, "- jq: ") {
				t.Fatalf("expected jq unmarked, got:\n%s", got)
			}
			if !strings.Contains(got, "- rg [needs rg]") {
				t.Fatalf("expected rg marked, got:\n%s", got)
			}
		})
	})
}

// TestListingJSONNamesTheDependencyAndAvailability pins the wire field names
// the consumer reads, and that an in-process tool carries an empty dependency
// rather than no key, so no consumer has to branch on the key's presence.
func TestListingJSONNamesTheDependencyAndAvailability(t *testing.T) {
	withListingUniverse(t, []string{"jq"}, func() {
		ctx, stdout := jsonListingContext(t)
		if err := List(ctx); err != nil {
			t.Fatalf("List: %v", err)
		}
		var raw []map[string]any
		if err := json.Unmarshal(stdout.Bytes(), &raw); err != nil {
			t.Fatalf("unmarshal the listing: %v\n%s", err, stdout.String())
		}
		byName := make(map[string]map[string]any, len(raw))
		for _, entry := range raw {
			name, _ := entry["name"].(string)
			byName[name] = entry
		}

		ripgrep, ok := byName["rg"]
		if !ok {
			t.Fatalf("the listing holds no rg:\n%s", stdout.String())
		}
		if ripgrep["dependency"] != "rg" {
			t.Fatalf("rg dependency = %v, want %q", ripgrep["dependency"], "rg")
		}
		if ripgrep["available"] != false {
			t.Fatalf("rg available = %v, want false", ripgrep["available"])
		}

		native, ok := byName["apply_patch"]
		if !ok {
			t.Fatalf("the listing holds no apply_patch:\n%s", stdout.String())
		}
		dependency, has := native["dependency"]
		if !has {
			t.Fatalf("apply_patch carries no dependency key at all")
		}
		if dependency != "" {
			t.Fatalf("apply_patch dependency = %v, want empty", dependency)
		}
		if native["available"] != true {
			t.Fatalf("apply_patch available = %v, want true", native["available"])
		}
	})
}

// TestListingKeepsHiddenToolsOut pins that the wider catalog changes nothing
// about hidden tools: they stay callable by name and out of the listing.
func TestListingKeepsHiddenToolsOut(t *testing.T) {
	withListingUniverse(t, everyProgram(), func() {
		listed := listingJSON(t)
		for _, b := range builtins() {
			if !b.hidden {
				continue
			}
			name := b.tool.Specification().Name
			for _, entry := range listed {
				if entry.Name == name {
					t.Fatalf("hidden tool %q is listed", name)
				}
			}
			if _, ok := Registry.Get(name); !ok {
				t.Fatalf("hidden tool %q must stay registered for profiles that name it", name)
			}
		}
	})
}

// TestDetailDescribesABuiltinThisMachineCannotLoad pins that the listing's
// wider set stays inspectable: a marked tool has a spec here too.
func TestDetailDescribesABuiltinThisMachineCannotLoad(t *testing.T) {
	withListingUniverse(t, nil, func() {
		var detailErr error
		got := captureSubCmdOut(t, func() {
			detailErr = Detail(procenv.Context(context.Background()), "rg")
		})
		if detailErr != nil {
			t.Fatalf("Detail: %v", detailErr)
		}
		if !strings.Contains(got, `"name": "rg"`) {
			t.Fatalf("expected rg's catalog specification, got:\n%s", got)
		}
	})
}

// TestDetailStillRefusesAHiddenBuiltinThisMachineCannotLoad pins that the
// widest reach of Detail is the visible catalog: a hidden worker tool stays
// reachable only while the registry holds it, as before.
func TestDetailStillRefusesAHiddenBuiltinThisMachineCannotLoad(t *testing.T) {
	withListingUniverse(t, nil, func() {
		var detailErr error
		captureSubCmdOut(t, func() {
			detailErr = Detail(procenv.Context(context.Background()), "clai_run")
		})
		if detailErr == nil {
			t.Fatal("expected an unloadable hidden built-in to stay unreachable")
		}
		if !strings.Contains(detailErr.Error(), "tool 'clai_run' not found") {
			t.Fatalf("unexpected error: %v", detailErr)
		}
	})
}

// TestBuiltinCatalogHoldsEveryShippedTool pins the catalog against a literal
// list rather than against itself: the listing's whole value is that it is the
// same on every machine, so a built-in that silently leaves the catalog must
// fail a test. Add the tool here when you add it to builtins().
func TestBuiltinCatalogHoldsEveryShippedTool(t *testing.T) {
	wantVisible := []string{
		"apply_patch",
		"async_cmd",
		"async_cmd_await",
		"async_cmd_cancel",
		"async_cmd_logs",
		"async_cmd_status",
		"audio_record",
		"audio_sources",
		"audio_transcribe",
		"cat",
		"cmd",
		"cp",
		"date",
		"ffprobe",
		"file_tree",
		"file_type",
		"find",
		"git",
		"go",
		"head",
		"jq",
		"line_count",
		"load_skill",
		"ls",
		"mkdir",
		"mktemp",
		"pwd",
		"rg",
		"rows_between",
		"rsync",
		"sed",
		"tail",
		"view_image",
		"website_text",
		"write_file",
	}
	if got := visibleBuiltinNames(); !slices.Equal(got, wantVisible) {
		t.Fatalf("the visible catalog is %v, want the shipped set %v", got, wantVisible)
	}

	wantHidden := []string{"clai_check", "clai_help", "clai_result", "clai_run", "clai_wait_for_workers"}
	var hidden []string
	for _, b := range builtins() {
		if b.hidden {
			hidden = append(hidden, b.tool.Specification().Name)
		}
	}
	slices.Sort(hidden)
	if !slices.Equal(hidden, wantHidden) {
		t.Fatalf("the hidden catalog is %v, want %v", hidden, wantHidden)
	}
}

// TestBuiltinCatalogHasOneEntryPerToolName keeps the catalog the single source
// for registration and listing: two entries for one name would list twice.
func TestBuiltinCatalogHasOneEntryPerToolName(t *testing.T) {
	seen := make(map[string]string, len(builtins()))
	for _, b := range builtins() {
		name := b.tool.Specification().Name
		if name == "" {
			t.Fatalf("catalog entry for executable %q has no tool name", b.executable)
		}
		if previous, ok := seen[name]; ok {
			t.Fatalf("%q is catalogued twice, with executables %q and %q", name, previous, b.executable)
		}
		seen[name] = b.executable
	}
}
