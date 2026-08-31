package bio

import (
	"bufio"
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// FSUsage watches disks through fs_usage(1), which reports the same kdebug
// events the buffer cache raises for every completed disk I/O:
//
//	13:02:55.798868  WrData[A]  D=0x0e2db501  B=0x100000 /dev/disk3s5  big.bin ...
//
// D is a block number in the whole disk's address space and B is the byte
// count, which is all the map needs.  Unlike the DTrace io provider this
// works with System Integrity Protection left on, which is why it is the
// default on macOS.
//
// Two things it cannot do: there is no TRIM in the kdebug vocabulary, so
// the amber layer stays empty, and there is no aggregation in the kernel,
// so a line crosses the pipe for every I/O.
type FSUsage struct {
	Disks      []Disk
	IntervalMS int
	Buckets    int

	stderr safeBuilder

	mu    sync.Mutex
	geom  map[string]Disk // by whole disk name
	cells map[cellKey]int64
	stats map[statKey]stat
}

type cellKey struct {
	dev    string
	cmd    Command
	bucket int
}

type statKey struct {
	dev string
	cmd Command
}

type stat struct {
	ops   int64
	bytes int64
}

// Start runs fs_usage until the context is cancelled.
func (f *FSUsage) Start(ctx context.Context) (<-chan Frame, <-chan error, error) {
	interval := f.IntervalMS
	if interval <= 0 {
		interval = 100
	}
	if f.Buckets <= 0 {
		f.Buckets = DefaultBuckets
	}

	f.geom = make(map[string]Disk, len(f.Disks))
	for _, d := range f.Disks {
		f.geom[d.Name] = d
	}
	f.cells = make(map[cellKey]int64)
	f.stats = make(map[statKey]stat)

	cmd := exec.CommandContext(ctx, "fs_usage", "-w", "-f", "diskio")
	cmd.Stderr = &f.stderr

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, nil, fmt.Errorf("fs_usage: %w", err)
	}

	frames := make(chan Frame, 8)
	errs := make(chan error, 1)
	done := make(chan struct{})

	go func() {
		defer close(done)
		scan := bufio.NewScanner(stdout)
		scan.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for scan.Scan() {
			f.add(scan.Text())
		}
	}()

	// fs_usage has no notion of a frame, so the clock is ours: drain what
	// has accumulated every interval, the way the D script's tick does.
	go func() {
		defer close(frames)
		tick := time.NewTicker(time.Duration(interval) * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-done:
				return
			case <-tick.C:
			}
			// If the display is behind, let the activity accumulate
			// into the next frame rather than dropping it: the kernel
			// does the same for the D script's aggregations.
			if len(frames) == cap(frames) {
				continue
			}
			frames <- f.drain()
		}
	}()

	go func() {
		defer close(errs)
		err := cmd.Wait()
		if ctx.Err() != nil { // we asked it to stop
			errs <- nil
			return
		}
		if err != nil {
			errs <- fmt.Errorf("fs_usage: %w: %s", err, f.stderr.String())
			return
		}
		errs <- nil
	}()

	return frames, errs, nil
}

// Warnings returns whatever fs_usage has written to stderr so far.
func (f *FSUsage) Warnings() string { return f.stderr.String() }

// add folds one fs_usage line into the frame being accumulated.
func (f *FSUsage) add(line string) {
	dev, cmd, block, bytes, ok := parseDiskIO(line)
	if !ok {
		return
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	d, ok := f.geom[dev]
	if !ok || d.MediaSize <= 0 || d.SectorSize <= 0 {
		return // not watched
	}
	bucket := int(block * d.SectorSize * int64(f.Buckets) / d.MediaSize)
	if bucket < 0 || bucket >= f.Buckets {
		return
	}

	f.cells[cellKey{dev, cmd, bucket}] += bytes
	s := f.stats[statKey{dev, cmd}]
	s.ops++
	s.bytes += bytes
	f.stats[statKey{dev, cmd}] = s
}

// drain turns what has accumulated since the last tick into a frame.
func (f *FSUsage) drain() Frame {
	f.mu.Lock()
	defer f.mu.Unlock()

	var frame Frame
	for k, bytes := range f.cells {
		frame.Cells = append(frame.Cells, Cell{k.dev, k.cmd, k.bucket, bytes})
		delete(f.cells, k)
	}
	for k, s := range f.stats {
		frame.Stats = append(frame.Stats, Stat{k.dev, k.cmd, s.ops, s.bytes})
		delete(f.stats, k)
	}
	return frame
}

// parseDiskIO reads one "fs_usage -f diskio" line.  The device is reported
// as the volume the I/O went to (disk3s5, disk3s1s1); the block number is
// in the whole disk's address space, so that is what comes back.
func parseDiskIO(line string) (dev string, cmd Command, block, bytes int64, ok bool) {
	f, n := head5(line)
	if n < 5 {
		return
	}

	switch {
	case strings.HasPrefix(f[1], "Rd"):
		cmd = CmdRead
	case strings.HasPrefix(f[1], "Wr"):
		cmd = CmdWrite
	default:
		return
	}

	block, ok = hexValue(f[2], "D=")
	if !ok {
		return "", 0, 0, 0, false
	}
	bytes, ok = hexValue(f[3], "B=")
	if !ok || bytes <= 0 {
		return "", 0, 0, 0, false
	}

	// "/dev/NOTFOUND" is what a write to something that is not a disk gets.
	dev = wholeDisk(f[4])
	if dev == "" {
		return "", 0, 0, 0, false
	}
	return dev, cmd, block, bytes, true
}

// hexValue reads a "D=0x1234" style field.
func hexValue(field, prefix string) (int64, bool) {
	s, ok := strings.CutPrefix(field, prefix)
	if !ok {
		return 0, false
	}
	s = strings.TrimPrefix(s, "0x")
	n, err := strconv.ParseInt(s, 16, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

// wholeDisk maps /dev/disk3s1s1 to disk3, and anything else to "".
func wholeDisk(path string) string {
	name, ok := strings.CutPrefix(path, "/dev/")
	if !ok {
		return ""
	}
	rest, ok := strings.CutPrefix(name, "disk")
	if !ok {
		return ""
	}
	i := 0
	for i < len(rest) && rest[i] >= '0' && rest[i] <= '9' {
		i++
	}
	if i == 0 {
		return ""
	}
	return name[:len("disk")+i]
}

// head5 splits off at most the first five whitespace-separated fields, so
// the pathname column costs nothing to skip and nothing is allocated for a
// line that arrives once per I/O.
func head5(line string) ([5]string, int) {
	var f [5]string
	got := 0
	for i := 0; i < len(line) && got < len(f); {
		for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
			i++
		}
		start := i
		for i < len(line) && line[i] != ' ' && line[i] != '\t' {
			i++
		}
		if i > start {
			f[got] = line[start:i]
			got++
		}
	}
	return f, got
}
