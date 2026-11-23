package audio

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/baalimago/clai/internal/audio/voices"
	"github.com/baalimago/clai/internal/diag"
)

const (
	// namingSilence separates two neighbours of a naming request.
	namingSilence = 500 * time.Millisecond
	// namingFormat is every part of a request before the parts are joined.
	namingFormat = "aformat=sample_fmts=s16:sample_rates=16000:channel_layouts=mono"
	// A clip lands when one label covers 4/5 of its region's labelled time.
	landedNum, landedDen = 4, 5
)

// Namer names the speakers of a transcribed recording after the people of a
// selection. A naming request is one audio file: the picked library clips,
// then one candidate clip of each transcript speaker cut from the recording,
// separated by silence. The transcriber's speaker labels over that file say
// which candidate sounds like which clip. Two requests are sent, with the
// picks in opposite orders, and a speaker takes a person's name only when
// both point at that person.
//
// The transcriber is reached only through Transcribe, and only a segment's
// start, end and speaker label are read, so naming works with any vendor.
type Namer struct {
	Transcriber Transcriber
	Runner      CommandRunner
	Budgets     Budgets
}

// namerPick is one pick of the selection and the index of its person.
type namerPick struct {
	voices.Pick
	person int
}

// namerSpeaker is one transcript label and its candidate clip, nil when no
// run of its speech is long enough.
type namerSpeaker struct {
	label     string
	candidate *Candidate
}

// Name never fails: whatever goes wrong leaves the segments as transcribed
// and is in the report. It leaves no file behind and writes nothing under
// voices; a clip's audio goes nowhere but into the request file.
func (n Namer) Name(ctx context.Context, recording string, segments []Segment, selection voices.Selection) Naming {
	naming := Naming{Segments: make([]NamedSegment, len(segments)), Report: NamingReport{Origin: selection.Origin}}
	for i, s := range segments {
		naming.Segments[i] = NamedSegment{Segment: s}
	}
	report := &naming.Report
	if selection.Empty() {
		report.Status = NamingNotAsked
		return naming
	}
	var picks []namerPick
	for person, v := range selection.Voices {
		report.Voices = append(report.Voices, VoiceNaming{Name: string(v.Name), Result: VoiceNotCompared})
		for _, p := range v.Picks {
			picks = append(picks, namerPick{Pick: p, person: person})
		}
	}
	for _, name := range selection.Unknown {
		report.Voices = append(report.Voices, VoiceNaming{Name: name, Result: VoiceUnknown})
	}
	speakers := namerSpeakers(segments)
	for _, s := range speakers {
		reason := ReasonNotCompared
		if s.candidate == nil {
			reason = ReasonNoCandidate
		}
		report.Speakers = append(report.Speakers, SpeakerNaming{Label: s.label, Reason: reason})
	}
	switch {
	case selection.Unreadable != nil:
		report.Status, report.Err = NamingLibraryUnreadable, selection.Unreadable
	case len(picks) == 0:
		report.Status = NamingNoVoice
	case len(speakers) == 0:
		report.Status = NamingNotDiarized
	case !slices.ContainsFunc(speakers, func(s namerSpeaker) bool { return s.candidate != nil }):
		report.Status = NamingNoCandidate
	}
	if report.Status != "" {
		return naming
	}
	compared, err := n.fit(picks, speakers)
	var answers [2][]Segment
	if err == nil {
		answers, err = n.ask(ctx, recording, picks, namerCuts(speakers, compared))
	}
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil && !errors.Is(err, ctxErr) {
			err = &NamingRequestError{Order: OrderForward, Stage: StageBuild, Err: ctxErr}
		}
		report.Status, report.Err = NamingRequestFailed, err
		return naming
	}
	naming.Outcomes = namerDecide(report, selection, picks, speakers, compared, answers)
	named := map[string]SpeakerNaming{}
	for _, s := range report.Speakers {
		if s.Name != "" {
			named[s.Label] = s
		}
	}
	for i := range naming.Segments {
		if s, ok := named[naming.Segments[i].Speaker]; ok {
			naming.Segments[i].Speaker, naming.Segments[i].Voice = string(s.Name), s.Voice
		}
	}
	return naming
}

// namerSpeakers lists the transcript's labels in order of appearance, each
// with the first candidate SampleExtractor finds for it.
func namerSpeakers(segments []Segment) []namerSpeaker {
	var speakers []namerSpeaker
	for _, s := range segments {
		if s.Speaker == "" || slices.ContainsFunc(speakers, func(k namerSpeaker) bool { return k.label == s.Speaker }) {
			continue
		}
		speaker := namerSpeaker{label: s.Speaker}
		if cands := (SampleExtractor{}).Candidates(segments, s.Speaker); len(cands) > 0 {
			speaker.candidate = &cands[0]
		}
		speakers = append(speakers, speaker)
	}
	return speakers
}

func pickLength(p voices.Pick) time.Duration {
	return time.Duration(math.Round(p.Seconds * float64(time.Second)))
}

func (c SourceInterval) length() time.Duration { return c.End - c.Start }

// fit returns the indices of the speakers whose candidates go into the
// requests, in transcript order. The picks always go in; candidates are
// taken most speech first while the request stays within the duration
// budget.
func (n Namer) fit(picks []namerPick, speakers []namerSpeaker) ([]int, error) {
	length := -namingSilence
	for _, p := range picks {
		length += namingSilence + pickLength(p.Pick)
	}
	var bySpeech []int
	for i, s := range speakers {
		if s.candidate != nil {
			bySpeech = append(bySpeech, i)
		}
	}
	slices.SortStableFunc(bySpeech, func(a, b int) int {
		return cmp.Compare(speakers[b].candidate.Speech, speakers[a].candidate.Speech)
	})
	var compared []int
	for _, i := range bySpeech {
		next := length + namingSilence + speakers[i].candidate.Clip.length()
		if next > n.Budgets.MaxRequestDuration {
			if len(compared) == 0 {
				return nil, &NamingRequestError{Order: OrderForward, Stage: StageBuild, Err: &NamingBudgetError{
					NeedDuration: next, LimitDuration: n.Budgets.MaxRequestDuration,
				}}
			}
			break
		}
		length = next
		compared = append(compared, i)
	}
	slices.Sort(compared)
	return compared, nil
}

func namerCuts(speakers []namerSpeaker, compared []int) []SourceInterval {
	cuts := make([]SourceInterval, len(compared))
	for j, i := range compared {
		cuts[j] = speakers[i].candidate.Clip
	}
	return cuts
}

// ask builds the forward and the reversed request, then sends both at once;
// the error is the forward order's unless it only failed for the cancel.
func (n Namer) ask(ctx context.Context, recording string, picks []namerPick, cuts []SourceInterval) ([2][]Segment, error) {
	var answers [2][]Segment
	if _, err := n.Runner.LookPath(ffmpegBin); err != nil {
		return answers, &voices.MissingToolError{Tool: ffmpegBin}
	}
	dir, err := os.MkdirTemp("", namingDirPrefix)
	if err != nil {
		return answers, &NamingRequestError{Order: OrderForward, Stage: StageBuild, Err: diag.Errorf("create the naming request directory: %w", err)}
	}
	defer os.RemoveAll(dir)
	paths := make([]string, len(picks))
	for i, p := range picks {
		paths[i] = p.Path
	}
	orders := [2]Order{OrderForward, OrderReversed}
	var files [2]string
	for i, order := range orders {
		files[i] = filepath.Join(dir, namingFile(order))
		if err := n.build(ctx, files[i], recording, order, paths, cuts); err != nil {
			return answers, err
		}
		slices.Reverse(paths)
	}
	// The other order's failure cancels sendCtx; only the run's ctx replaces a cause.
	sendCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var errs [2]error
	// cancelled: an order that failed for the other's cancel, not for itself.
	var cancelled [2]bool
	var wg sync.WaitGroup
	for i, order := range orders {
		wg.Go(func() {
			answers[i], errs[i] = n.Transcriber.Transcribe(sendCtx, files[i])
			if errs[i] = cmp.Or(errs[i], ctx.Err()); errs[i] != nil {
				cancelled[i] = sendCtx.Err() != nil && ctx.Err() == nil && errors.Is(errs[i], context.Canceled)
				errs[i] = namingFailure(ctx, order, StageSend, errs[i])
				cancel()
			}
		})
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil && !cancelled[i] {
			return answers, err
		}
	}
	return answers, cmp.Or(errs[0], errs[1])
}

const namingDirPrefix = "clai-naming-"

func namingFile(order Order) string { return string(order) + ".flac" }

// namingOrderOf is the order of a naming request file, false for any other
// file.
func namingOrderOf(path string) (Order, bool) {
	if !strings.HasPrefix(filepath.Base(filepath.Dir(path)), namingDirPrefix) {
		return "", false
	}
	for _, order := range []Order{OrderForward, OrderReversed} {
		if filepath.Base(path) == namingFile(order) {
			return order, true
		}
	}
	return "", false
}

func namingFailure(ctx context.Context, order Order, stage NamingRequestStage, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		err = ctxErr
	}
	return &NamingRequestError{Order: order, Stage: stage, Err: err}
}

// build writes one order's request file at path.
func (n Namer) build(ctx context.Context, path, recording string, order Order, picks []string, cuts []SourceInterval) error {
	fail := func(err error) error { return namingFailure(ctx, order, StageBuild, err) }
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	if _, stderr, err := n.Runner.Run(ctx, ffmpegBin, namingArgs(recording, picks, cuts, path)...); err != nil {
		detail := strings.TrimSpace(stderr)
		if detail = strings.TrimSpace(detail[strings.LastIndexByte(detail, '\n')+1:]); detail != "" {
			detail = ": " + detail
		}
		return fail(diag.Errorf("ffmpeg: %w%s", err, detail))
	}
	info, err := os.Stat(path)
	switch {
	case err != nil:
		return fail(diag.Errorf("ffmpeg wrote no naming request: %w", err))
	case info.Size() == 0:
		return fail(errors.New("ffmpeg wrote an empty naming request " + path))
	case info.Size() > n.Budgets.MaxRequestBytes:
		return fail(&NamingBudgetError{NeedBytes: info.Size(), LimitBytes: n.Budgets.MaxRequestBytes})
	}
	return nil
}

// namingArgs is the one ffmpeg invocation that writes a request to out, its
// last argument: the picks whole, then the cuts of the recording, with
// silence between neighbours and no other audio.
func namingArgs(recording string, picks []string, cuts []SourceInterval, out string) []string {
	seconds := func(d time.Duration) string { return strconv.FormatFloat(d.Seconds(), 'f', -1, 64) }
	args := []string{"-nostdin", "-hide_banner", "-loglevel", "error", "-y"}
	for _, p := range picks {
		args = append(args, "-i", p)
	}
	for _, c := range cuts {
		args = append(args, "-ss", seconds(c.Start), "-t", seconds(c.length()), "-i", recording)
	}
	inputs := len(picks) + len(cuts)
	var graph, chain strings.Builder
	for i := range inputs {
		fmt.Fprintf(&graph, "[%d:a]%s[a%d];", i, namingFormat, i)
		if i > 0 {
			fmt.Fprintf(&graph, "aevalsrc=exprs=0:channel_layout=mono:sample_rate=16000:duration=%s,%s[g%d];",
				seconds(namingSilence), namingFormat, i-1)
			fmt.Fprintf(&chain, "[g%d]", i-1)
		}
		fmt.Fprintf(&chain, "[a%d]", i)
	}
	fmt.Fprintf(&graph, "%sconcat=n=%d:v=0:a=1[out]", chain.String(), 2*inputs-1)
	return append(args, "-filter_complex", graph.String(), "-map", "[out]", "-map_metadata", "-1", "-c:a", "flac", out)
}

// namerRegion is one clip's or candidate's place in an answer: the label
// that covers most of its labelled time, "" for none or a tie, and whether
// that label's share makes the clip land.
type namerRegion struct {
	label  string
	landed bool
}

func regionOf(answer []Segment, from, to time.Duration) namerRegion {
	byLabel := map[string]time.Duration{}
	var total, most time.Duration
	for _, s := range answer {
		if overlap := min(s.End, to) - max(s.Start, from); s.Speaker != "" && overlap > 0 {
			byLabel[s.Speaker] += overlap
			total += overlap
		}
	}
	var top string
	tied := false
	for label, d := range byLabel {
		switch {
		case d > most:
			top, most, tied = label, d, false
		case d == most:
			tied = true
		}
	}
	if total == 0 || tied {
		return namerRegion{}
	}
	return namerRegion{label: top, landed: int64(most)*landedDen >= int64(total)*landedNum}
}

// namerAnswer is one order's answer read by region: picks by their index in
// the selection, candidates in request order.
type namerAnswer struct {
	picks      []namerRegion
	candidates []namerRegion
}

func readAnswer(answer []Segment, picks []namerPick, order Order, cuts []SourceInterval) namerAnswer {
	read := namerAnswer{picks: make([]namerRegion, len(picks)), candidates: make([]namerRegion, len(cuts))}
	at := -namingSilence
	next := func(length time.Duration) namerRegion {
		at += namingSilence
		region := regionOf(answer, at, at+length)
		at += length
		return region
	}
	for i := range picks {
		if order == OrderReversed {
			i = len(picks) - 1 - i
		}
		read.picks[i] = next(pickLength(picks[i].Pick))
	}
	for j, c := range cuts {
		read.candidates[j] = next(c.length())
	}
	return read
}

// pointsAt is the person a candidate's label points at in one order, -1 for
// nobody: at least one of the person's clips landed with that label, and no
// clip of another person, landed or not, has it.
func (a namerAnswer) pointsAt(picks []namerPick, label string) (person int, ambiguous bool) {
	person = -1
	landed := false
	for i, p := range picks {
		if label == "" || a.picks[i].label != label {
			continue
		}
		if person >= 0 && person != p.person {
			return -1, true
		}
		person = p.person
		landed = landed || a.picks[i].landed
	}
	if !landed {
		return -1, false
	}
	return person, false
}

// namerDecide names the speakers both answers agree on in the report and
// returns how each pick fared.
func namerDecide(report *NamingReport, selection voices.Selection, picks []namerPick, speakers []namerSpeaker, compared []int, answers [2][]Segment) []voices.ClipOutcome {
	cuts := namerCuts(speakers, compared)
	read := [2]namerAnswer{
		readAnswer(answers[0], picks, OrderForward, cuts),
		readAnswer(answers[1], picks, OrderReversed, cuts),
	}
	for i := range selection.Voices {
		report.Voices[i].Result = VoiceNotPlaced
	}
	outcomes := make([]voices.ClipOutcome, len(picks))
	for i, p := range picks {
		outcomes[i] = voices.ClipOutcome{Clip: p.Ref, Landed: read[0].picks[i].landed || read[1].picks[i].landed}
		if outcomes[i].Landed {
			report.Voices[p.person].Result = VoiceAbsent
		}
	}
	report.Status = NamingNoAgreement
	for j, i := range compared {
		labels := [2]string{read[0].candidates[j].label, read[1].candidates[j].label}
		forward, forwardAmbiguous := read[0].pointsAt(picks, labels[0])
		reversed, reversedAmbiguous := read[1].pointsAt(picks, labels[1])
		speaker := &report.Speakers[i]
		switch {
		case forward >= 0 && forward == reversed:
			source := namerSource(read, picks, forward, labels)
			speaker.Name, speaker.Reason = selection.Voices[forward].Name, ""
			speaker.Voice = &Provenance{Clip: picks[source].Ref, Status: picks[source].Status}
			outcomes[source].Named = append(outcomes[source].Named, speaker.Label)
			report.Voices[forward].Result = VoiceNamed
			report.Status = NamingNamed
		case forwardAmbiguous || reversedAmbiguous:
			speaker.Reason = ReasonAmbiguous
		case forward != reversed:
			speaker.Reason = ReasonDisagreed
		default:
			speaker.Reason = ReasonNoMatch
		}
	}
	return outcomes
}

// namerSource is the pick a name comes from: of the person's picks that
// landed with the candidate's label in at least one order, the first
// confirmed one, else the first. So a name reads as unconfirmed only when
// no confirmed clip supports it.
func namerSource(read [2]namerAnswer, picks []namerPick, person int, labels [2]string) int {
	first := -1
	for i, p := range picks {
		forward, reversed := read[0].picks[i], read[1].picks[i]
		supports := forward.landed && forward.label == labels[0] || reversed.landed && reversed.label == labels[1]
		if p.person != person || !supports {
			continue
		}
		if p.Status == voices.Confirmed {
			return i
		}
		if first < 0 {
			first = i
		}
	}
	return first
}
