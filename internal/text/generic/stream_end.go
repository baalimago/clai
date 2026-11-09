package generic

import (
	"io"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/baalimago/clai/internal/diag"
	"github.com/baalimago/clai/pkg/claierr"
)

// bodyExcerptLimit bounds how much of a response body an error carries.
const bodyExcerptLimit = 512

// bodyExcerptCut marks an excerpt that is shorter than its body.
const bodyExcerptCut = " […]"

// BodyExcerpt is the start of body, at most bodyExcerptLimit bytes of valid
// UTF-8, for an error that shows what a provider sent.
func BodyExcerpt(body []byte) string {
	text := strings.TrimSpace(strings.ToValidUTF8(string(body), "\uFFFD"))
	if len(text) <= bodyExcerptLimit {
		return text
	}
	cut := bodyExcerptLimit
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut] + bodyExcerptCut
}

// excerptBuffer keeps the first bytes of a stream for BodyExcerpt.
type excerptBuffer struct{ head []byte }

func (b *excerptBuffer) add(chunk []byte) {
	if room := 2*bodyExcerptLimit - len(b.head); room > 0 {
		b.head = append(b.head, chunk[:min(room, len(chunk))]...)
	}
}

// NoCompletionError reports an HTTP 200 whose body was not a completion
// stream: the provider returned no completion.
func NoCompletionError(body []byte) error {
	return claierr.NewUnexpectedProviderResponse(http.StatusOK, []byte("the provider returned no completion: "+bodyDescription(body)))
}

func bodyDescription(body []byte) string {
	excerpt := BodyExcerpt(body)
	if excerpt == "" {
		return "the response body is empty"
	}
	return "the response body is not a completion stream: " + excerpt
}

// TruncatedError reports a stream that ended before marker, the terminator
// its protocol guarantees: the reply is cut short. It is a transport error
// matching io.ErrUnexpectedEOF.
func TruncatedError(marker string) error {
	return claierr.NewTransport(diag.Errorf("the reply is truncated: the stream ended before %s: %w", marker, io.ErrUnexpectedEOF))
}
