package vendors

// export_test.go opens the discovery seams to the package's external tests.
// It compiles only under `go test`, so nothing in production can reach them.

// SetDiscoverWorkers pins the pool bound and returns the restore function. A
// test that widens or narrows the pool must defer the result, or the next
// test inherits the bound.
func SetDiscoverWorkers(n int) func() {
	previous := discoverWorkers
	discoverWorkers = func() int { return n }
	return func() { discoverWorkers = previous }
}
