package bio

import (
	"strings"
	"testing"
)

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

func TestScriptCarriesGeometry(t *testing.T) {
	s := Script([]Disk{{Name: "ada0", MediaSize: 1000204886016}}, 100, 4096)
	for _, want := range []string{
		`media["ada0"] = 1000204886016;`,
		`track["ada0"] = 1;`,
		"tick-100ms",
		"inline int BUCKETS = 4096;",
		"int64_t media[string];",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("script is missing %q", want)
		}
	}
	// %@d must survive the Sprintf that builds the script.
	if !strings.Contains(s, `printa("C %s %d %d %@d\n", @cells);`) {
		t.Errorf("printa line mangled:\n%s", s)
	}
}

func TestHumanBytes(t *testing.T) {
	cases := map[int64]string{
		0:             "0B",
		999:           "999B",
		1024:          "1.0K",
		10 * 1024:     "10K",
		1000204886016: "932G",
	}
	for in, want := range cases {
		if got := HumanBytes(in); got != want {
			t.Errorf("HumanBytes(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestLessDevice(t *testing.T) {
	if !lessDevice("ada2", "ada10") {
		t.Error("ada2 should sort before ada10")
	}
	if !lessDevice("ada10", "nvd0") {
		t.Error("ada10 should sort before nvd0")
	}
}
