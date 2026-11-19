// Package voicescmd is the `clai audio voices` command: `add` enrols a clip
// by hand and `list` browses the library as a menu of three levels, the
// people, one person's clips and one clip, entered by a path of arguments.
package voicescmd

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/baalimago/clai/internal"
	"github.com/baalimago/clai/internal/audio/voices"
	"github.com/baalimago/clai/internal/diag"
	"github.com/baalimago/clai/internal/runenv"
	"github.com/baalimago/go_away_boilerplate/pkg/cmd"
)

// Key is the key `audio` holds the command under: the verb and its short
// name.
const Key = "voices|v"

const (
	addUsage  = "usage: clai audio v|voices a|add <wav> [-from s] [-to s] [-name n]"
	listUsage = "usage: clai audio v|voices l|list [person [clip [action [args…]]]] [-json]"
	synopsis  = `usage:
  clai audio v|voices a|add <wav> [-from s] [-to s] [-name n]
  clai audio v|voices l|list [person [clip [action [args…]]]] [-json]

  add     adds a clip of one person's voice: <wav> is a clip already cut, or a
          recording to cut one from between -from and -to, in seconds; -name
          names the person (by default the file's name)
  list    shows the people, one person's clips, or one clip; its arguments are
          a path: a person, a clip (a prefix of its id, or its number) and an
          action: play | confirm | overrule | trim <from> <to> |
          move <person> | delete
  -json   prints the level the path names as JSON`
)

// Library is the part of the voice library the command works on.
type Library interface {
	List() ([]voices.VoiceSummary, error)
	Enrol(voices.Enrolment) (voices.Enrolled, error)
	Confirm(voices.ClipRef) (voices.Confirmation, error)
	Overrule(voices.ClipRef) (voices.ClipSummary, error)
	Remove(voices.ClipRef) error
}

// Deps is what one run works with.
type Deps struct {
	Library Library
	// Inspect cuts and measures a clip before it is enrolled.
	Inspect func(ctx context.Context, file string, cut voices.Cut) (voices.Inspection, error)
	// Runner finds and runs the program that plays a clip.
	Runner voices.Runner
	// Width returns the terminal's width; nil means the width of stdout.
	Width func() int
}

// Opener returns the dependencies of one run. It is called once the
// arguments are known to be usable, never for a usage error or for help.
type Opener func(ctx context.Context) (Deps, error)

// UsageError says a voices command cannot be run as written.
type UsageError struct{ Line string }

func (e *UsageError) Error() string { return e.Line }
func (e *UsageError) ExitCode() int { return 2 }

type command struct {
	open           Opener
	from, to, name internal.StringFlag
	json           internal.BoolFlag
}

// Command returns `voices` with its verbs `add`, short `a`, and `list`,
// short `l`.
func Command(open Opener) *internal.Command {
	v := &command{open: open}
	add := &internal.Command{
		Name:     "add",
		Desc:     "Add a clip of a person's voice to the library",
		HelpText: addUsage,
		Register: func(fs *flag.FlagSet) {
			v.from.Register(fs, "Where the clip starts in <wav>, in decimal seconds (default: its start).", "from")
			v.to.Register(fs, "Where the clip ends in <wav>, in decimal seconds (default: its end).", "to")
			v.name.Register(fs, "The person; needed with -from or -to (default: the file's name).", "name")
		},
		OnRun: func(ctx context.Context, c *internal.Command) error { return exitStatus(v.add(ctx, c)) },
	}
	list := &internal.Command{
		Name:     "list",
		Desc:     "Browse the library: the people, one person's clips, one clip and its actions",
		HelpText: listUsage,
		Register: func(fs *flag.FlagSet) {
			v.json.Register(fs, "Print the level the path names as JSON; never a menu.", "json")
		},
		OnRun: func(ctx context.Context, c *internal.Command) error { return exitStatus(v.list(ctx, c)) },
	}
	return &internal.Command{
		Name:     "voices",
		Desc:     "Keep the library of known voices: a|add <wav>, l|list [person [clip [action]]]",
		HelpText: synopsis,
		Subs:     map[string]cmd.Command{"add|a": add, "list|l": list},
		OnRun:    func(context.Context, *internal.Command) error { return &UsageError{Line: synopsis} },
	}
}

// exitStatus keeps a usage error's 2 and makes every other failure 1, also
// one that wraps a player's *exec.ExitError, which carries a status of its
// own.
func exitStatus(err error) error {
	if _, usage := errors.AsType[*UsageError](err); err == nil || usage {
		return err
	}
	return internal.StatusError(1, err)
}

func (v *command) add(ctx context.Context, c *internal.Command) error {
	defer func() { v.from, v.to, v.name = internal.StringFlag{}, internal.StringFlag{}, internal.StringFlag{} }()
	files, err := positionals(c, addUsage)
	if err != nil {
		return err
	}
	switch len(files) {
	case 0:
		return &UsageError{Line: "add needs the audio file to take the clip from, <wav>; " + addUsage}
	case 1:
	default:
		return &UsageError{Line: fmt.Sprintf("add takes one audio file, got %d: %s; %s", len(files), strings.Join(files, ", "), addUsage)}
	}
	var cut voices.Cut
	for _, bound := range []struct {
		flag  string
		value internal.StringFlag
		into  *time.Duration
	}{{"-from", v.from, &cut.From}, {"-to", v.to, &cut.To}} {
		if !bound.value.Explicit() {
			continue
		}
		if *bound.into, err = parseSeconds(bound.value.Value()); err != nil {
			return &UsageError{Line: fmt.Sprintf("%s %q is not a decimal number of seconds, such as 61.2; %s", bound.flag, bound.value.Value(), addUsage)}
		}
	}
	name := v.name.Value()
	switch {
	case (v.from.Explicit() || v.to.Explicit()) && !v.name.Explicit():
		return &UsageError{Line: "-from and -to cut a clip out of a recording, so the person must be named: add -name <person>"}
	case v.to.Explicit() && cut.To <= cut.From:
		from := v.from.Value()
		if from == "" {
			from = "0"
		}
		return &UsageError{Line: fmt.Sprintf("-to %s ends the clip before -from %s starts it; %s", v.to.Value(), from, addUsage)}
	case !v.name.Explicit():
		name = nameOfFile(files[0])
	}
	deps, err := v.open(ctx)
	if err != nil {
		return err
	}
	inspection, err := deps.Inspect(ctx, files[0], cut)
	if err != nil {
		return err
	}
	enrolled, err := deps.Library.Enrol(voices.Enrolment{
		Name: name, Inspection: inspection, Status: voices.Confirmed, Source: voices.SourceAdd,
	})
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(runenv.From(ctx).Stdout(), "%s: clip %s added (%s)\n", name, enrolled.Ref.ID, voices.Confirmed); err != nil {
		return diag.Errorf("voices add: write the result: %w", err)
	}
	if enrolled.Leftover != nil {
		runenv.Warnf(ctx, runenv.SubsystemAudio, "%s", enrolled.Leftover.Error())
	}
	return nil
}

// errNotSeconds says a text is not a plain decimal number of seconds.
var errNotSeconds = errors.New("not a decimal number of seconds")

// parseSeconds reads a decimal number of seconds exactly, without the
// rounding of a float.
func parseSeconds(text string) (time.Duration, error) {
	digits := strings.TrimLeft(text, "+-")
	whole, fraction, _ := strings.Cut(digits, ".")
	isDigits := func(s string) bool { return strings.Trim(s, "0123456789") == "" }
	if len(text)-len(digits) > 1 || whole+fraction == "" || !isDigits(whole) || !isDigits(fraction) {
		return 0, errNotSeconds
	}
	d, err := time.ParseDuration(text + "s")
	if err != nil {
		return 0, errNotSeconds
	}
	return d, nil
}

// nameOfFile is the name a clip already cut takes from its file: the base
// name without its extension, its first letter upper-cased.
func nameOfFile(file string) string {
	base := filepath.Base(file)
	base = strings.TrimSuffix(base, filepath.Ext(base))
	if base == "" {
		return base
	}
	r, size := utf8.DecodeRuneInString(base)
	return string(unicode.ToUpper(r)) + base[size:]
}

// positionals returns the verb's positional arguments and sets the flags
// that stand among or after them. The dispatcher parsed the flags before
// the first positional; it stops at a flag-shaped first argument only after
// "--", so such an argument starts the positionals.
func positionals(c *internal.Command, usage string) ([]string, error) {
	args := c.Args()[min(1, len(c.Args())):]
	flagShaped := func(arg string) bool { return len(arg) > 1 && arg[0] == '-' }
	if len(args) > 0 && flagShaped(args[0]) {
		return args, nil
	}
	fs := c.Flagset()
	var out []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			return append(out, args[i+1:]...), nil
		}
		if !flagShaped(arg) {
			out = append(out, arg)
			continue
		}
		name, value, inline := strings.Cut(strings.TrimLeft(arg, "-"), "=")
		f := fs.Lookup(name)
		if f == nil {
			return nil, &UsageError{Line: fmt.Sprintf("%s is not a flag of this verb; %s", arg, usage)}
		}
		if boolean, ok := f.Value.(interface{ IsBoolFlag() bool }); !inline && ok && boolean.IsBoolFlag() {
			value = "true"
		} else if !inline {
			if i+1 >= len(args) {
				return nil, &UsageError{Line: fmt.Sprintf("%s needs a value; %s", arg, usage)}
			}
			i++
			value = args[i]
		}
		if err := fs.Set(name, value); err != nil {
			return nil, &UsageError{Line: fmt.Sprintf("%s %s: %v; %s", arg, value, err, usage)}
		}
	}
	return out, nil
}
