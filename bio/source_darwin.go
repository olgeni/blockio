package bio

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// NewSource returns the backend that will watch the given disks.  fs_usage
// is the default because it works on a stock machine; dtrace is finer but
// needs System Integrity Protection relaxed first.
func NewSource(kind SourceKind, disks []Disk, intervalMS, buckets int) (Source, error) {
	switch kind {
	case SourceAuto, SourceFSUsage:
		return &FSUsage{Disks: disks, IntervalMS: intervalMS, Buckets: buckets}, nil
	case SourceDtrace:
		return &Tracer{Disks: disks, IntervalMS: intervalMS, Buckets: buckets}, nil
	}
	return nil, fmt.Errorf("source %q is not available on macOS (fsusage, dtrace)", kind)
}

// CheckSource explains the usual reasons a backend will not start.
func CheckSource(kind SourceKind) error {
	if os.Geteuid() != 0 {
		return errors.New("needs root: try sudo blockio")
	}
	if kind == SourceDtrace {
		return checkIOProvider()
	}
	if _, err := exec.LookPath("fs_usage"); err != nil {
		return errors.New("fs_usage(1) is missing")
	}
	return nil
}

// checkIOProvider asks dtrace whether it can see the io provider at all.
// It usually cannot: System Integrity Protection hides every kernel probe,
// and /dev/dtrace is there either way, so it is no use as a test.
func checkIOProvider() error {
	out, err := exec.Command("dtrace", "-l", "-n", "io:::start").CombinedOutput()
	if err == nil && listsProbe(string(out), "io") {
		return nil
	}
	return errors.New("dtrace cannot see the io provider: System Integrity " +
		"Protection hides kernel probes.  Boot into recoveryOS and run " +
		"`csrutil enable --without dtrace` (on Apple silicon lower the " +
		"security policy to Reduced Security first), or leave -source at " +
		"its default and read fs_usage(1) instead")
}

// listsProbe reports whether a "dtrace -l" listing names the provider, as
// opposed to just printing its header.
func listsProbe(listing, provider string) bool {
	for _, line := range strings.Split(listing, "\n") {
		f := strings.Fields(line)
		if len(f) >= 2 && f[1] == provider {
			return true
		}
	}
	return false
}
