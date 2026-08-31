//go:build !freebsd

package bio

import (
	"fmt"
	"runtime"
)

// errUnsupported is what everything that has to talk to the kernel returns
// on a platform with no DTrace io provider.
var errUnsupported = fmt.Errorf("blockio does not know how to watch disks on %s (FreeBSD only)", runtime.GOOS)

func Disks() ([]Disk, error) { return nil, errUnsupported }

func Info(string) (Disk, error) { return Disk{}, errUnsupported }

func NewSource(SourceKind, []Disk, int, int) (Source, error) { return nil, errUnsupported }

func CheckSource(SourceKind) error { return errUnsupported }
