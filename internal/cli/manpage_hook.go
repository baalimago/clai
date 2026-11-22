//go:build !wasix

package cli

// FirstRunManPage is the hook main sets: the first run of a release installs
// its man page.
func FirstRunManPage() ManPageHook { return ensureManPage }
