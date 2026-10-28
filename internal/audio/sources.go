package audio

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/baalimago/clai/internal/capture"
	pkgtools "github.com/baalimago/clai/pkg/tools"
)

func sourcesText(ctx context.Context, r capture.Recorder) (string, error) {
	sources, err := r.Sources(ctx)
	if err != nil {
		return "", err
	}
	systemAudio := "unavailable"
	for _, s := range sources {
		if s.Kind == capture.Sink {
			systemAudio = "available"
			break
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "system_audio: %s\n", systemAudio)
	for _, s := range sources {
		fmt.Fprintf(&b, "%s %s [%s]", s.Kind, strconv.Quote(s.Name), s.Label)
		if s.Default {
			b.WriteString(" default")
		}
		b.WriteString("\n")
	}
	return b.String(), nil
}

func init() {
	pkgtools.AudioSourcesEngine = func(ctx context.Context) (string, error) {
		return sourcesText(ctx, capture.New())
	}
}
