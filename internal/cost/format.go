package cost

import (
	"fmt"
	"strings"
)

func FormatUSD(v float64) string {
	s := fmt.Sprintf("%.3f", v)
	s = strings.TrimRight(s, "0")
	s = strings.TrimRight(s, ".")
	if s == "" {
		s = "0"
	}
	return "$" + s
}
