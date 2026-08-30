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

	// Pin the display: the tests are about what gets drawn, not about what
	// the terminal running them happens to support.
	cfg := DefaultConfig()
	cfg.Color = Color256
	cfg.HalfBlocks = false
	cfg.Buckets = 1024

	m := New(disks, frames, errs, 100*time.Millisecond, cfg)
	m.SetSize(80, 24)
	return m
}

func TestReadsAreGreenWritesAreRed(t *testing.T) {
	m := testModel(bio.Disk{Name: "ada0", MediaSize: 1 << 40, Rotation: -1})
	m.Apply(bio.Frame{
		Cells: []bio.Cell{
			{Dev: "ada0", Cmd: bio.CmdRead, Bucket: 0, Bytes: 1 << 20},
			{Dev: "ada0", Cmd: bio.CmdWrite, Bucket: 1023, Bytes: 1 << 20},
		},
		Stats: []bio.Stat{{Dev: "ada0", Cmd: bio.CmdRead, Ops: 10, Bytes: 1 << 20}},
	})

	view := m.View()
	for _, want := range []int{ramp256[idxRead][4], ramp256[idxWrite][4], idle256} {
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

func TestFixedThresholdsAreAbsolute(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Scale = ScaleFixed
	cfg.Thresholds = []float64{1 << 10, 1 << 20, 1 << 30, 1 << 40}

	cases := []struct {
		rate float64
		want float64
	}{
		{0, 0},
		{512, 0},
		{2 << 10, 0.25},
		{2 << 20, 0.5},
		{2 << 40, 1},
	}
	for _, c := range cases {
		// The peak is irrelevant with fixed thresholds.
		if got := heatLevel(c.rate, 1<<50, cfg); got != c.want {
			t.Errorf("heatLevel(%g) = %g, want %g", c.rate, got, c.want)
		}
	}
}

func TestHalfBlocksDoubleTheRows(t *testing.T) {
	m := testModel(bio.Disk{Name: "ada0", MediaSize: 1 << 40, Rotation: -1})
	m.cfg.HalfBlocks = true
	dev := m.devs[0]

	// One read at the very top of the device: with half blocks the first
	// terminal row covers twice as much, so the mark lands in a foreground
	// (top half) color and the row still has a background color set.
	dev.heat[idxRead][0] = 1 << 20
	lines := renderMap(dev, 40, 4, m.cfg)
	if len(lines) != 4 {
		t.Fatalf("got %d lines, want 4", len(lines))
	}
	if !strings.Contains(lines[0], "▀") || !strings.Contains(lines[0], "\x1b[48;5;") {
		t.Errorf("first row is not half blocks: %q", lines[0])
	}
}

func TestColorModeParsing(t *testing.T) {
	for in, want := range map[string]ColorMode{
		"auto": ColorAuto, "truecolor": ColorTrue, "256": Color256,
		"16": Color16, "off": ColorNone,
	} {
		got, err := ParseColorMode(in)
		if err != nil || got != want {
			t.Errorf("ParseColorMode(%q) = %v, %v", in, got, err)
		}
	}
	if _, err := ParseColorMode("chartreuse"); err == nil {
		t.Error("a bad color mode should be an error")
	}
}

func TestThresholdParsing(t *testing.T) {
	got, err := ParseThresholds("64K, 512K,4M,1G")
	if err != nil {
		t.Fatal(err)
	}
	want := []float64{64 << 10, 512 << 10, 4 << 20, 1 << 30}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("threshold %d = %g, want %g", i, got[i], want[i])
		}
	}
	for _, bad := range []string{"", "4M,1M", "burnt sienna"} {
		if _, err := ParseThresholds(bad); err == nil {
			t.Errorf("ParseThresholds(%q) should fail", bad)
		}
	}
}

func TestPaintKeepsColorSpacesApart(t *testing.T) {
	// 82 is a green in the 256-color cube and bright green in ANSI: the
	// escape must say which one is meant.
	p256 := paint{kind: paint256, code: 82}
	if got, want := p256.fg(), "\x1b[38;5;82m"; got != want {
		t.Errorf("256 fg = %q, want %q", got, want)
	}
	if got, want := p256.bg(), "\x1b[48;5;82m"; got != want {
		t.Errorf("256 bg = %q, want %q", got, want)
	}

	pansi := paint{kind: paintANSI, code: 32}
	if got, want := pansi.fg(), "\x1b[32m"; got != want {
		t.Errorf("ansi fg = %q, want %q", got, want)
	}
	if got, want := pansi.bg(), "\x1b[42m"; got != want {
		t.Errorf("ansi bg = %q, want %q", got, want)
	}

	prgb := paint{kind: paintRGB, rgb: [3]float64{1, 2, 3}}
	if got, want := prgb.fg(), "\x1b[38;2;1;2;3m"; got != want {
		t.Errorf("rgb fg = %q, want %q", got, want)
	}
}
