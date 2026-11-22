package audio

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/baalimago/clai/internal/audio/voices"
)

// NamingStatus is the outcome of one naming as a whole. Only NamingNamed
// changes a speaker; with every status before NamingRequestFailed nothing
// was run and nothing was sent.
type NamingStatus string

const (
	// NamingNotAsked: the selection was empty.
	NamingNotAsked NamingStatus = "not-asked"
	// NamingLibraryUnreadable: people were named and the library could not
	// be read.
	NamingLibraryUnreadable NamingStatus = "library-unreadable"
	// NamingNoVoice: every person named is unknown to the library.
	NamingNoVoice NamingStatus = "no-voice"
	// NamingNotDiarized: the transcript has no speaker label.
	NamingNotDiarized NamingStatus = "not-diarized"
	// NamingNoCandidate: no speaker talks long enough to cut a clip from.
	NamingNoCandidate NamingStatus = "no-candidate"
	// NamingRequestFailed: a naming request could not be built or was not
	// answered.
	NamingRequestFailed NamingStatus = "request-failed"
	// NamingNoAgreement: both requests were answered and nobody took a name.
	NamingNoAgreement NamingStatus = "no-agreement"
	NamingNamed       NamingStatus = "named"
)

// SpeakerReason says why a transcript speaker kept its label.
type SpeakerReason string

const (
	// ReasonNoCandidate: no run of the speaker's speech is long enough.
	ReasonNoCandidate SpeakerReason = "no-candidate"
	// ReasonNoMatch: in neither order did a person's clip share the
	// speaker's label.
	ReasonNoMatch SpeakerReason = "no-match"
	// ReasonAmbiguous: in an order, clips of two people shared its label.
	ReasonAmbiguous SpeakerReason = "ambiguous"
	// ReasonDisagreed: the two orders pointed at different people, or only
	// one pointed at anyone.
	ReasonDisagreed SpeakerReason = "disagreed"
	// ReasonNotCompared: no naming was answered, or the request budget left
	// the speaker's candidate out.
	ReasonNotCompared SpeakerReason = "not-compared"
)

// VoiceResult says how one person of the selection fared.
type VoiceResult string

const (
	VoiceNamed VoiceResult = "named"
	// VoiceAbsent: a clip of the person landed and named nobody.
	VoiceAbsent VoiceResult = "absent"
	// VoiceNotPlaced: no clip of the person landed in either order.
	VoiceNotPlaced VoiceResult = "not-placed"
	// VoiceNotCompared: no naming was answered.
	VoiceNotCompared VoiceResult = "not-compared"
	// VoiceUnknown: the library does not hold the person.
	VoiceUnknown VoiceResult = "unknown-voice"
)

// Provenance is the clip that named a segment and that clip's status when
// it was picked: the segment's voice field.
type Provenance struct {
	Clip   voices.ClipRef `json:"clip"`
	Status voices.Status  `json:"status"`
}

// NamedSegment is one transcript segment after naming: Speaker is the
// person's name when a clip named it, and Voice that clip.
type NamedSegment struct {
	Segment
	Voice *Provenance
}

// SpeakerNaming is what became of one transcript speaker: a Name with the
// Voice it came from, or the Reason it kept its label.
type SpeakerNaming struct {
	Label  string        `json:"label"`
	Name   voices.Name   `json:"name,omitempty"`
	Voice  *Provenance   `json:"voice,omitempty"`
	Reason SpeakerReason `json:"reason,omitempty"`
}

// VoiceNaming is what became of one person the run was asked to name. Name
// is the stored name, or for VoiceUnknown the name as given.
type VoiceNaming struct {
	Name   string      `json:"name"`
	Result VoiceResult `json:"result"`
}

// Order is the sequence of the picked clips in a naming request. Each
// recording is named in both, and a name applies only when they agree.
type Order string

const (
	OrderForward  Order = "forward"
	OrderReversed Order = "reversed"
)

// NamingRequestStage is the step of a naming request that failed.
type NamingRequestStage string

const (
	StageBuild NamingRequestStage = "build"
	StageSend  NamingRequestStage = "send"
)

type NamingRequestError struct {
	Order Order
	Stage NamingRequestStage
	Err   error
}

func (e *NamingRequestError) Error() string {
	return fmt.Sprintf("naming request (%s order) failed to %s: %v", e.Order, e.Stage, e.Err)
}
func (e *NamingRequestError) Unwrap() error { return e.Err }

// NamingBudgetError says a naming request does not fit the request budget:
// the picked clips with one candidate are too long, or the built file is
// too large.
type NamingBudgetError struct {
	NeedDuration  time.Duration
	LimitDuration time.Duration
	NeedBytes     int64
	LimitBytes    int64
}

func (e *NamingBudgetError) Error() string {
	if e.NeedBytes > 0 || e.LimitBytes > 0 {
		return fmt.Sprintf("naming request is %d bytes, over the request budget of %d bytes (transcribe.max-request-bytes)",
			e.NeedBytes, e.LimitBytes)
	}
	return fmt.Sprintf("naming request needs %v with one candidate, over the request budget of %v (transcribe.max-request-seconds)",
		e.NeedDuration, e.LimitDuration)
}

// NamingReport is the outcome of one naming beside the segments: the naming
// object of the json output.
type NamingReport struct {
	Status NamingStatus
	Origin voices.Origin
	// Speakers has one entry per transcript label, in order of appearance.
	Speakers []SpeakerNaming
	// Voices has one entry per selected person, then one per unknown one.
	Voices []VoiceNaming
	// Err is why the naming was not answered.
	Err error
	// Unrecorded is why the clips' outcomes were not tallied.
	Unrecorded error
}

func (r NamingReport) MarshalJSON() ([]byte, error) {
	message := func(err error) *string {
		if err == nil {
			return nil
		}
		text := err.Error()
		return &text
	}
	return json.Marshal(struct {
		Status     NamingStatus    `json:"status"`
		Origin     voices.Origin   `json:"origin"`
		Speakers   []SpeakerNaming `json:"speakers"`
		Voices     []VoiceNaming   `json:"voices"`
		Error      *string         `json:"error,omitempty"`
		Unrecorded *string         `json:"unrecorded,omitempty"`
	}{r.Status, r.Origin, append([]SpeakerNaming{}, r.Speakers...), append([]VoiceNaming{}, r.Voices...), message(r.Err), message(r.Unrecorded)})
}

// Naming is the result of naming one recording.
type Naming struct {
	Segments []NamedSegment
	Report   NamingReport
	// Outcomes is how each picked clip fared, for the library's tally. It
	// is empty unless both requests were answered.
	Outcomes []voices.ClipOutcome
}
