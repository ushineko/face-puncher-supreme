//go:build !linux

package probe

// countOpenFDs is not supported on this platform.
func countOpenFDs() int { return -1 }

// getMaxFDs is not supported on this platform.
func getMaxFDs() int { return -1 }
