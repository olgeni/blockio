//go:build darwin || windows

package bio

import "testing"

// A frame is what the buckets and the geometry make of the I/Os.
func TestAccumulator(t *testing.T) {
	a := newAccumulator([]Disk{{Name: "disk1", SectorSize: 512, MediaSize: 1 << 40}}, 1024)

	a.add("disk1", CmdWrite, 1<<39, 0x1000) // half way in
	a.add("disk1", CmdWrite, 1<<39+0x1000, 0x1000)
	a.addBlock("disk1", CmdRead, 1, 0x4000) // sector 1 is still bucket 0
	a.add("disk1", CmdRead, 1<<40, 0x1000)  // past the end
	a.add("disk1", CmdRead, -1, 0x1000)     // before the start
	a.add("disk9", CmdRead, 0, 0x1000)      // not watched

	frame := a.drain()
	if len(frame.Cells) != 2 {
		t.Fatalf("cells = %v, want one per bucket touched", frame.Cells)
	}
	for _, c := range frame.Cells {
		switch {
		case c.Cmd == CmdWrite && (c.Bucket != 512 || c.Bytes != 0x2000):
			t.Errorf("write cell = %v, want bucket 512 and both I/Os summed", c)
		case c.Cmd == CmdRead && (c.Bucket != 0 || c.Bytes != 0x4000):
			t.Errorf("read cell = %v, want bucket 0", c)
		}
	}
	for _, s := range frame.Stats {
		switch {
		case s.Cmd == CmdWrite && (s.Ops != 2 || s.Bytes != 0x2000):
			t.Errorf("write stat = %v", s)
		case s.Cmd == CmdRead && (s.Ops != 1 || s.Bytes != 0x4000):
			t.Errorf("read stat = %v, want the I/Os off the disk left out", s)
		}
	}

	if got := a.drain(); len(got.Cells) != 0 || len(got.Stats) != 0 {
		t.Errorf("second drain = %v, want an empty frame", got)
	}
}
