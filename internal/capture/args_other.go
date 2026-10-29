//go:build !linux && !darwin

package capture

func inputArgsForSources(srcs []Source) []string { return nil }
