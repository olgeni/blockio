//go:build darwin || windows

package bio

import (
	"context"
	"sync"
	"time"
)

// accumulator does in Go what the D script's aggregations do in the kernel,
// for the backends that report one I/O at a time (fs_usage on macOS, ETW on
// Windows): every I/O is folded into a map under a mutex, and a ticker
// drains the map into a frame at the sampling interval.
type accumulator struct {
	buckets int
	geom    map[string]Disk // by name, never written after construction

	mu    sync.Mutex
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

func newAccumulator(disks []Disk, buckets int) *accumulator {
	if buckets <= 0 {
		buckets = DefaultBuckets
	}
	a := &accumulator{
		buckets: buckets,
		geom:    make(map[string]Disk, len(disks)),
		cells:   make(map[cellKey]int64),
		stats:   make(map[statKey]stat),
	}
	for _, d := range disks {
		a.geom[d.Name] = d
	}
	return a
}

// add folds one I/O, at a byte offset from the start of the disk, into the
// frame being accumulated.
func (a *accumulator) add(dev string, cmd Command, offset, bytes int64) {
	d, ok := a.geom[dev]
	if !ok || offset < 0 || offset >= d.MediaSize {
		return // not watched, or not on the disk
	}
	bucket := int(offset * int64(a.buckets) / d.MediaSize)

	a.mu.Lock()
	defer a.mu.Unlock()

	a.cells[cellKey{dev, cmd, bucket}] += bytes
	s := a.stats[statKey{dev, cmd}]
	s.ops++
	s.bytes += bytes
	a.stats[statKey{dev, cmd}] = s
}

// addBlock is add for a position counted in the disk's own sectors.
func (a *accumulator) addBlock(dev string, cmd Command, block, bytes int64) {
	if d, ok := a.geom[dev]; ok && d.SectorSize > 0 {
		a.add(dev, cmd, block*d.SectorSize, bytes)
	}
}

// drain turns what has accumulated since the last tick into a frame.
func (a *accumulator) drain() Frame {
	a.mu.Lock()
	defer a.mu.Unlock()

	var frame Frame
	for k, bytes := range a.cells {
		frame.Cells = append(frame.Cells, Cell{k.dev, k.cmd, k.bucket, bytes})
		delete(a.cells, k)
	}
	for k, s := range a.stats {
		frame.Stats = append(frame.Stats, Stat{k.dev, k.cmd, s.ops, s.bytes})
		delete(a.stats, k)
	}
	return frame
}

// run drains a frame every interval until the context is cancelled or done
// closes, and closes the channel it returns when it stops.
func (a *accumulator) run(ctx context.Context, interval time.Duration, done <-chan struct{}) <-chan Frame {
	frames := make(chan Frame, 8)
	go func() {
		defer close(frames)
		tick := time.NewTicker(interval)
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
			frames <- a.drain()
		}
	}()
	return frames
}
