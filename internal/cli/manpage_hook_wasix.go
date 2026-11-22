//go:build wasix

package cli

// FirstRunManPage is nil under wasix; see architecture/manpage.md.
func FirstRunManPage() ManPageHook { return nil }
