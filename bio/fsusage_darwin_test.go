package bio

import "testing"

func TestParseDiskIO(t *testing.T) {
	cases := []struct {
		line  string
		dev   string
		cmd   Command
		block int64
		bytes int64
		ok    bool
	}{
		{
			line: "13:02:55.798868    WrData[A]       D=0x0e2db501  B=0x100000 /dev/disk3s5  big.bin   0.000150 W dd.38325373",
			dev:  "disk3", cmd: CmdWrite, block: 0x0e2db501, bytes: 0x100000, ok: true,
		},
		{
			line: "13:02:55.798666    RdMeta[S]       D=0x00008ae0  B=0x1000   /dev/disk3  /dev/disk3    0.000745 W dd.38325373",
			dev:  "disk3", cmd: CmdRead, block: 0x8ae0, bytes: 0x1000, ok: true,
		},
		{
			// a snapshot is still its whole disk's address space
			line: "13:02:55.798666    RdData[ST2]     D=0x00000010  B=0x4000   /dev/disk3s1s1  Foo Bar/baz  0.1 W x.1",
			dev:  "disk3", cmd: CmdRead, block: 0x10, bytes: 0x4000, ok: true,
		},
		{
			// not a disk
			line: "13:02:55.804465    WrData[S]       D=0x0150a800  B=0x10000  /dev/NOTFOUND  /dev/null   0.000001 W Syncthing.1",
		},
		{line: "13:02:55.804465    lseek             F=17   O=0x00000000   0.000004   fseventsd.101"},
		{line: ""},
		{line: "13:02:55.798868    WrData[A]       D=zzz  B=0x100000 /dev/disk3s5  big.bin"},
	}

	for _, c := range cases {
		dev, cmd, block, bytes, ok := parseDiskIO(c.line)
		if ok != c.ok {
			t.Errorf("parseDiskIO(%q) ok = %v, want %v", c.line, ok, c.ok)
			continue
		}
		if !ok {
			continue
		}
		if dev != c.dev || cmd != c.cmd || block != c.block || bytes != c.bytes {
			t.Errorf("parseDiskIO(%q) = %s %v %d %d, want %s %v %d %d",
				c.line, dev, cmd, block, bytes, c.dev, c.cmd, c.block, c.bytes)
		}
	}
}

func TestWholeDisk(t *testing.T) {
	for in, want := range map[string]string{
		"/dev/disk3":      "disk3",
		"/dev/disk3s5":    "disk3",
		"/dev/disk13s1s1": "disk13",
		"/dev/NOTFOUND":   "",
		"/dev/null":       "",
		"disk3":           "",
	} {
		if got := wholeDisk(in); got != want {
			t.Errorf("wholeDisk(%q) = %q, want %q", in, got, want)
		}
	}
}

// A frame is what the buckets and the geometry make of the raw lines.
func TestFSUsageFrames(t *testing.T) {
	f := &FSUsage{
		Disks:   []Disk{{Name: "disk3", SectorSize: 4096, MediaSize: 1 << 40}},
		Buckets: 1024,
	}
	f.geom = map[string]Disk{"disk3": f.Disks[0]}
	f.cells = map[cellKey]int64{}
	f.stats = map[statKey]stat{}

	// half way into the device: block 128Mi of 256Mi blocks
	f.add("00:00:00.1 WrData[A] D=0x8000000 B=0x1000 /dev/disk3s5 big.bin 0.1 W dd.1")
	f.add("00:00:00.2 WrData[A] D=0x8000000 B=0x1000 /dev/disk3s5 big.bin 0.1 W dd.1")
	f.add("00:00:00.3 RdData[A] D=0x0 B=0x4000 /dev/disk3 x 0.1 W dd.1")
	f.add("00:00:00.4 WrData[A] D=0x1000 B=0x1000 /dev/disk9 x 0.1 W dd.1") // not watched

	frame := f.drain()
	if len(frame.Cells) != 2 {
		t.Fatalf("cells = %v, want one per bucket touched", frame.Cells)
	}
	var write, read Cell
	for _, c := range frame.Cells {
		if c.Cmd == CmdWrite {
			write = c
		} else {
			read = c
		}
	}
	if write.Bucket != 512 || write.Bytes != 2*0x1000 {
		t.Errorf("write cell = %v, want bucket 512 and both I/Os summed", write)
	}
	if read.Bucket != 0 || read.Bytes != 0x4000 {
		t.Errorf("read cell = %v, want bucket 0", read)
	}

	for _, s := range frame.Stats {
		if s.Cmd == CmdWrite && (s.Ops != 2 || s.Bytes != 2*0x1000) {
			t.Errorf("write stat = %v", s)
		}
	}
	if got := f.drain(); len(got.Cells) != 0 || len(got.Stats) != 0 {
		t.Errorf("second drain = %v, want an empty frame", got)
	}
}
