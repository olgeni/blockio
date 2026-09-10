//go:build !freebsd && !darwin && !(windows && (amd64 || arm64))

package bio

import (
	"fmt"
	"runtime"
)

// errUnsupported is what everything that has to talk to the kernel returns
// on a platform with none of the DTrace io provider, fs_usage(1) and ETW.
var errUnsupported = fmt.Errorf("blockio does not know how to watch disks on %s/%s (FreeBSD, macOS and 64-bit Windows only)", runtime.GOOS, runtime.GOARCH)

func Disks() ([]Disk, error) { return nil, errUnsupported }

func Info(string) (Disk, error) { return Disk{}, errUnsupported }

func NewSource(SourceKind, []Disk, int, int) (Source, error) { return nil, errUnsupported }

func CheckSource(SourceKind) error { return errUnsupported }
