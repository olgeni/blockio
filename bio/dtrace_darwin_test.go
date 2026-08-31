package bio

import (
	"os"
	"strings"
	"syscall"
	"testing"
)

func TestScriptCarriesGeometry(t *testing.T) {
	table := "\tmedia[7] = 994662584320;\n\tbsize[7] = 4096;\n\tname[7] = \"disk3\";\n\ttrack[7] = 1;\n"
	s := buildScript(table, 100, 4096)
	for _, want := range []string{
		"media[7] = 994662584320;",
		`name[7] = "disk3";`,
		"tick-100ms",
		"inline int BUCKETS = 4096;",
		"/track[args[1]->dev_minor] != 0/",
		"(args[0]->b_flags & B_READ) != 0 ? 1 : 2;", // CmdRead, CmdWrite
	} {
		if !strings.Contains(s, want) {
			t.Errorf("script is missing %q:\n%s", want, s)
		}
	}
	// %@d must survive the Sprintf that builds the script.
	if !strings.Contains(s, `printa("C %s %d %d %@d\n", @cells);`) {
		t.Errorf("printa line mangled:\n%s", s)
	}
}

// The minors of a whole disk have to start with the disk's own, whatever
// this machine happens to be.
func TestMinorsStartWithTheDiskItself(t *testing.T) {
	info, err := os.Stat("/dev/disk0")
	if err != nil {
		t.Skip("no /dev/disk0 on this machine")
	}
	want := int(info.Sys().(*syscall.Stat_t).Rdev & 0xffffff)

	got := minors("disk0")
	if len(got) == 0 || got[0] != want {
		t.Errorf("minors(disk0) = %v, want it to start with %d", got, want)
	}
}
