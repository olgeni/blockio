//go:build freebsd || darwin

package bio

import "testing"

func TestParse(t *testing.T) {
	var f Frame
	parse("C ada0 1 512 4096", &f, 1024)
	parse("C ada0 2 1023 8192", &f, 1024)
	parse("C ada0 1 9999 8192", &f, 1024) // bucket out of range
	parse("S ada0 2 12 49152", &f, 1024)
	parse("junk", &f, 1024)
	parse("C ada0 1 x 4096", &f, 1024)

	want := []Cell{
		{"ada0", CmdRead, 512, 4096},
		{"ada0", CmdWrite, 1023, 8192},
	}
	if len(f.Cells) != len(want) {
		t.Fatalf("cells = %v, want %v", f.Cells, want)
	}
	for i, c := range f.Cells {
		if c != want[i] {
			t.Errorf("cell %d = %v, want %v", i, c, want[i])
		}
	}
	if len(f.Stats) != 1 || f.Stats[0] != (Stat{"ada0", CmdWrite, 12, 49152}) {
		t.Errorf("stats = %v", f.Stats)
	}
}
