//go:build amd64 || arm64

package bio

import (
	"errors"
	"fmt"

	"golang.org/x/sys/windows"
)

// NewSource returns the backend that will watch the given disks.  Windows
// has only the one.
func NewSource(kind SourceKind, disks []Disk, intervalMS, buckets int) (Source, error) {
	switch kind {
	case SourceAuto, SourceETW:
		return &ETW{Disks: disks, IntervalMS: intervalMS, Buckets: buckets}, nil
	}
	return nil, fmt.Errorf("source %q is not available on Windows (etw)", kind)
}

// CheckSource explains the usual reason ETW will not start: a kernel
// provider is only for an elevated process.
func CheckSource(kind SourceKind) error {
	if !windows.GetCurrentProcessToken().IsElevated() {
		return errors.New("needs an elevated prompt for ETW: run it from an Administrator terminal")
	}
	return nil
}
