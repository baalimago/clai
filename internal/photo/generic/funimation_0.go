package generic

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/baalimago/clai/internal/utils"
)

func StartAnimation() func() {
	t0 := time.Now()
	ticker := time.NewTicker(time.Second / 60)
	stop := make(chan struct{})
	// The animation clears the current row with spaces, so it needs the width
	// of the file it actually writes to: stdout. A non-terminal stdout yields
	// the deterministic fallback width.
	termWidth := utils.SessionDimensions(os.Stdout).Width
	go func() {
		for {
			select {
			case <-ticker.C:
				cTick := time.Since(t0)
				clearLine := strings.Repeat(" ", termWidth)
				fmt.Printf("\r%v", clearLine)
				fmt.Printf("\rElapsed time: %v - %v", funimation(cTick), cTick)
			case <-stop:
				return
			}
		}
	}()
	return func() {
		close(stop)
	}
}

func funimation(t time.Duration) string {
	images := []string{
		"🕛",
		"🕧",
		"🕐",
		"🕜",
		"🕑",
		"🕝",
		"🕒",
		"🕞",
		"🕓",
		"🕟",
		"🕔",
		"🕠",
		"🕕",
		"🕡",
		"🕖",
		"🕢",
		"🕗",
		"🕣",
		"🕘",
		"🕤",
		"🕙",
		"🕥",
		"🕚",
		"🕦",
	}
	// 1 nanosecond / 23 frames = 43478260 nanoseconds. Too low brainjuice to know
	// why that works right now
	return images[int(t.Nanoseconds()/43478260)%len(images)]
}
