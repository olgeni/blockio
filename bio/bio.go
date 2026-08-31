// Package bio discovers block devices and watches their I/O.
//
// What does the watching is per-platform: FreeBSD reads the DTrace io
// provider, macOS reads fs_usage(1) by default and DTrace on request.  A
// Source hides the difference; everything above it sees only Frames.
package bio

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
)

// DefaultBuckets is how finely each device is split when nothing else is
// asked for.  The map is downsampled to whatever the terminal gives us, so
// this is the ceiling on how fine it can ever be.
const DefaultBuckets = 4096

// BucketRange is what a bucket count is clamped to: below the first the map
// is coarse whatever the terminal does, above the second the source is
// reporting more than the display can use.
const (
	MinBuckets = 256
	MaxBuckets = 16384
)

// ClampBuckets rounds a wanted bucket count into range.
func ClampBuckets(n int) int {
	if n < MinBuckets {
		return MinBuckets
	}
	if n > MaxBuckets {
		return MaxBuckets
	}
	return n
}

// Disk is one block device.
type Disk struct {
	Name       string // "ada0", "disk3"
	SectorSize int64
	MediaSize  int64
	Sectors    int64
	Stripe     int64
	Descr      string // "Samsung SSD 870 EVO 1TB"
	Rotation   int    // 0 for solid state, -1 when unknown
}

// Size renders the media size the way diskinfo(8) does.
func (d Disk) Size() string { return HumanBytes(d.MediaSize) }

// Command is a bio_cmd value; see /usr/lib/dtrace/io.d.
type Command int

const (
	CmdRead   Command = 1
	CmdWrite  Command = 2
	CmdDelete Command = 3 // TRIM/UNMAP
	CmdFlush  Command = 5
)

func (c Command) String() string {
	switch c {
	case CmdRead:
		return "READ"
	case CmdWrite:
		return "WRITE"
	case CmdDelete:
		return "TRIM"
	case CmdFlush:
		return "FLUSH"
	}
	return "CMD" + strconv.Itoa(int(c))
}

// Cell is the bytes one command moved inside one bucket of one device.
type Cell struct {
	Dev    string
	Cmd    Command
	Bucket int
	Bytes  int64
}

// Stat totals one command on one device over the frame.
type Stat struct {
	Dev   string
	Cmd   Command
	Ops   int64
	Bytes int64
}

// Frame is one tick's worth of activity: what moved, and where.
type Frame struct {
	Cells []Cell
	Stats []Stat
}

// Source watches a set of disks and reports what they are doing, one Frame
// per interval.
type Source interface {
	// Start runs until the context is cancelled.  Frames arrive on the
	// first channel, which closes when watching stops; the error channel
	// then carries whatever went wrong, or nil.
	Start(ctx context.Context) (<-chan Frame, <-chan error, error)

	// Warnings returns whatever the backend has complained about so far.
	Warnings() string
}

// SourceKind names a backend.  Which ones exist depends on the platform;
// SourceAuto picks the one that works without being configured for.
type SourceKind string

const (
	SourceAuto    SourceKind = "auto"
	SourceDtrace  SourceKind = "dtrace"
	SourceFSUsage SourceKind = "fsusage"
)

// ParseSourceKind reads a -source value.
func ParseSourceKind(s string) (SourceKind, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "auto":
		return SourceAuto, nil
	case "dtrace":
		return SourceDtrace, nil
	case "fsusage", "fs_usage":
		return SourceFSUsage, nil
	}
	return SourceAuto, fmt.Errorf("bad source %q (auto, dtrace, fsusage)", s)
}

// safeBuilder collects a subprocess's stderr, which the process writes from
// one goroutine while Warnings reads it from another.
type safeBuilder struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (b *safeBuilder) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *safeBuilder) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return strings.TrimSpace(b.buf.String())
}

// HumanBytes renders a byte count in the usual binary units.
func HumanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%dB", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit && exp < 5; v /= unit {
		div *= unit
		exp++
	}
	v := float64(n) / float64(div)
	format := "%.0f%c"
	if v < 10 {
		format = "%.1f%c"
	}
	return fmt.Sprintf(format, v, "KMGTPE"[exp])
}

// lessDevice orders ada0 before ada10 before nvd0.
func lessDevice(a, b string) bool {
	pa, na := splitDevice(a)
	pb, nb := splitDevice(b)
	if pa != pb {
		return pa < pb
	}
	return na < nb
}

func splitDevice(s string) (string, int) {
	i := len(s)
	for i > 0 && s[i-1] >= '0' && s[i-1] <= '9' {
		i--
	}
	n, _ := strconv.Atoi(s[i:])
	return s[:i], n
}
