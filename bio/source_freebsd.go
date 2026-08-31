package bio

import (
	"errors"
	"fmt"
	"os"
)

// NewSource returns the backend that will watch the given disks.  FreeBSD
// has only the one.
func NewSource(kind SourceKind, disks []Disk, intervalMS, buckets int) (Source, error) {
	switch kind {
	case SourceAuto, SourceDtrace:
		return &Tracer{Disks: disks, IntervalMS: intervalMS, Buckets: buckets}, nil
	}
	return nil, fmt.Errorf("source %q is not available on FreeBSD (dtrace)", kind)
}

// CheckSource explains the two usual reasons dtrace will not start.
func CheckSource(kind SourceKind) error {
	if os.Geteuid() != 0 {
		return errors.New("needs root for dtrace(1): try sudo blockio")
	}
	if _, err := os.Stat("/dev/dtrace"); err != nil {
		return errors.New("/dev/dtrace is missing: kldload dtraceall")
	}
	return nil
}
