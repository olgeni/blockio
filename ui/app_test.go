package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/olgeni/blockio/bio"
)

func testModel(disks ...bio.Disk) Model {
	frames := make(chan bio.Frame)
	errs := make(chan error)
	m := New(disks, frames, errs, 100*time.Millisecond)
	m.SetSize(80, 24)
	return m
}

func TestReadsAreGreenWritesAreRed(t *testing.T) {
	m := testModel(bio.Disk{Name: "ada0", MediaSize: 1 << 40, Rotation: -1})
	m.Apply(bio.Frame{
		Cells: []bio.Cell{
			{Dev: "ada0", Cmd: bio.CmdRead, Bucket: 0, Bytes: 1 << 20},
			{Dev: "ada0", Cmd: bio.CmdWrite, Bucket: bio.Buckets - 1, Bytes: 1 << 20},
		},
		Stats: []bio.Stat{{Dev: "ada0", Cmd: bio.CmdRead, Ops: 10, Bytes: 1 << 20}},
	})

	view := m.View()
	for _, want := range []int{rampRead[len(rampRead)-1], rampWrite[len(rampWrite)-1], idleColor} {
		if !strings.Contains(view, fmt.Sprintf("\x1b[38;5;%dm", want)) {
			t.Errorf("view has no color %d", want)
		}
	}
	if !strings.Contains(view, "ada0") {
		t.Error("view does not name the device")
	}
}

func TestPanesAreLaidOutInTwoColumns(t *testing.T) {
	var disks []bio.Disk
	for i := 0; i < 3; i++ {
		disks = append(disks, bio.Disk{Name: fmt.Sprintf("ada%d", i), MediaSize: 1 << 40, Rotation: -1})
	}
	m := testModel(disks...)
	m.SetSize(120, 30)

	// Three devices occupy the four-pane layout: two panes on the top row.
	line := strings.Split(m.View(), "\n")[1]
	if n := strings.Count(line, "╭"); n != 2 {
		t.Errorf("top row has %d panes, want 2", n)
	}
}

func TestPauseFreezesTheMap(t *testing.T) {
	m := testModel(bio.Disk{Name: "ada0", MediaSize: 1 << 40, Rotation: -1})
	m.paused = true
	m.Apply(bio.Frame{Cells: []bio.Cell{{Dev: "ada0", Cmd: bio.CmdRead, Bucket: 0, Bytes: 1 << 20}}})
	if m.devs[0].heat[idxRead][0] != 0 {
		t.Error("a paused model should ignore frames")
	}
}
