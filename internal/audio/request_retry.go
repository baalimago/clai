package audio

import (
	"context"
	"errors"
	"fmt"

	"github.com/baalimago/clai/internal/resend"
	"github.com/baalimago/clai/internal/runenv"
	"github.com/baalimago/clai/pkg/claierr"
)

func retryText(t resend.Try) string {
	return fmt.Sprintf("try=%d/%d delay=%s cause=%q", t.Attempt, t.Of, secs(t.Delay), resend.Reason(t.Cause))
}

// retryState fits the board's state column: the status, or that none came.
func retryState(t resend.Try) string {
	var facts claierr.APIErrorer
	if errors.As(t.Cause, &facts) {
		return fmt.Sprintf("%v · %d", t, facts.API().StatusCode)
	}
	return t.String() + " · no answer"
}

// resending sends a request again on the schedule; state gets the row's text (architecture/audio.md, Request retries).
type resending struct {
	Transcriber
	schedule resend.Schedule
	request  string
	state    func(string)
}

func (r resending) Transcribe(ctx context.Context, filePath string) ([]Segment, error) {
	tries := 0
	return resend.Do(ctx, r.schedule, resend.Throttled, func(t resend.Try) {
		tries = t.Attempt
		runenv.Debugf(ctx, runenv.SubsystemAudio, "transcription retry request=%s %s", r.request, retryText(t))
		r.show(retryState(t))
	}, func(ctx context.Context) ([]Segment, error) {
		if tries > 0 {
			r.show(fmt.Sprintf("transcribing · try %d", tries+1))
		}
		return r.Transcriber.Transcribe(ctx, filePath)
	})
}

func (r resending) show(state string) {
	if r.state != nil {
		r.state(state)
	}
}
