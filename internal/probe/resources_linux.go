//go:build linux

package probe

import (
	"bufio"
	"os"
	"strconv"
	"strings"
)

// countOpenFDs returns the number of open file descriptors for the current
// process by counting entries in /proc/self/fd.
func countOpenFDs() int {
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		return -1
	}
	return len(entries)
}

// getMaxFDs returns the soft limit for open file descriptors by parsing
// /proc/self/limits.
func getMaxFDs() int {
	f, err := os.Open("/proc/self/limits")
	if err != nil {
		return -1
	}
	defer f.Close() //nolint:errcheck // best-effort

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "Max open files") {
			continue
		}
		fields := strings.Fields(line)
		// Format: "Max open files  <soft>  <hard>  <units>"
		// The soft limit is the 4th field (0-indexed: 3).
		if len(fields) < 5 {
			return -1
		}
		n, err := strconv.Atoi(fields[3])
		if err != nil {
			return -1
		}
		return n
	}
	return -1
}
