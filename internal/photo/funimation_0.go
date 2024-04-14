package photo

import (
	"fmt"
	"strings"
	"time"

	"github.com/baalimago/clai/internal/utils"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
)

func StartAnimation() func() {
	t0 := time.Now()
	ticker := time.NewTicker(time.Second / 60)
	stop := make(chan struct{})
	termWidth, err := utils.TermWidth()
	if err != nil {
		ancli.PrintWarn(fmt.Sprintf("failed to get terminal size: %v\n", err))
		termWidth = 100
	}
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
