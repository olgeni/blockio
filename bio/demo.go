package bio

import (
	"context"
	"math/rand"
	"time"
)

// Demo fabricates devices and traffic, so the layouts can be looked at on a
// machine that does not have eight disks (and without root).  Device 0 gets
// a sequential sweep, the kind a resilver or a scrub draws; the rest get
// scattered reads and writes.
func Demo(ctx context.Context, n int, interval time.Duration) ([]Disk, <-chan Frame, <-chan error) {
	disks := make([]Disk, 0, n)
	for i := 0; i < n; i++ {
		disks = append(disks, Disk{
			Name:       "demo" + string(rune('0'+i)),
			SectorSize: 512,
			MediaSize:  int64(1+i%3) * 1000204886016,
			Descr:      "synthetic device",
			Rotation:   -1,
		})
	}

	frames := make(chan Frame, 4)
	errs := make(chan error, 1)

	go func() {
		defer close(frames)
		defer close(errs)

		tick := time.NewTicker(interval)
		defer tick.Stop()

		sweep := 0
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
			}

			var f Frame
			for i, d := range disks {
				switch i % 3 {
				case 0: // a sweep marching across the device
					head := (sweep + i*40) % Buckets
					for b := head; b < head+6 && b < Buckets; b++ {
						f.Cells = append(f.Cells, Cell{d.Name, CmdRead, b, 16 << 20})
					}
					f.Stats = append(f.Stats, Stat{d.Name, CmdRead, 900, 96 << 20})
				case 1: // scattered writes
					for j := 0; j < 12; j++ {
						f.Cells = append(f.Cells, Cell{d.Name, CmdWrite, rand.Intn(Buckets), int64(rand.Intn(1 << 20))})
					}
					f.Stats = append(f.Stats, Stat{d.Name, CmdWrite, 120, 6 << 20})
				case 2: // a busy region plus the odd trim
					for j := 0; j < 20; j++ {
						f.Cells = append(f.Cells, Cell{d.Name, CmdRead, Buckets/3 + rand.Intn(Buckets/8), int64(rand.Intn(4 << 20))})
					}
					if rand.Intn(4) == 0 {
						f.Cells = append(f.Cells, Cell{d.Name, CmdDelete, rand.Intn(Buckets), 1 << 20})
						f.Stats = append(f.Stats, Stat{d.Name, CmdDelete, 3, 1 << 20})
					}
					f.Stats = append(f.Stats, Stat{d.Name, CmdRead, 400, 40 << 20})
				}
			}
			sweep = (sweep + 3) % Buckets

			select {
			case frames <- f:
			case <-ctx.Done():
				return
			}
		}
	}()

	return disks, frames, errs
}
