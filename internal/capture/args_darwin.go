//go:build darwin

package capture

func inputArgsForSources(srcs []Source) []string {
	args := make([]string, 0, len(srcs)*3)
	for _, s := range srcs {
		args = append(args, "-f", "avfoundation", "-i", ":"+s.Name)
	}
	return args
}
