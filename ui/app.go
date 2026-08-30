// Package ui draws block device activity as a defrag-style map: one cell
// per slice of the device, green for reads, red for writes, amber for TRIM.
package ui

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/olgeni/blockio/bio"
)

// The three tracked commands, in display order.
const (
	idxRead = iota
	idxWrite
	idxTrim
	nCmds
)

const trailFloor = 0.04

// Scale decides how a cell's byte rate becomes a step on the color ramp.
type Scale int

const (
	// ScaleAuto measures each device against its own busiest cell, so a
	// quiet disk still shows structure.
	ScaleAuto Scale = iota
	// ScaleFixed compares against the configured thresholds, so the same
	// color means the same rate on every device and at every moment.
	ScaleFixed
)

func (s Scale) String() string {
	if s == ScaleFixed {
		return "fixed"
	}
	return "auto"
}

// Config is what the display lets you tune: where hot turns into cold, and
// how long activity stays on the map.
type Config struct {
	Scale      Scale
	Thresholds []float64     // bytes per second, per cell, ascending
	HeatLife   time.Duration // half-life of activity
	TrailLife  time.Duration // half-life of the trail, 0 for none
	Color      ColorMode
	Buckets    int  // how finely each device is split
	HalfBlocks bool // two data rows per terminal row
}

// DefaultConfig is a ramp that suits a mixed workload on a single disk.
func DefaultConfig() Config {
	return Config{
		Scale:      ScaleAuto,
		Thresholds: []float64{64 << 10, 512 << 10, 4 << 20, 32 << 20},
		HeatLife:   500 * time.Millisecond,
		TrailLife:  60 * time.Second,
		Color:      ColorAuto,
		Buckets:    bio.DefaultBuckets,
		HalfBlocks: true,
	}
}

// ParseThresholds reads "64K,512K,4M,32M" into bytes per second.  Up to
// eight steps, ascending; the ramp is stretched over however many there are.
func ParseThresholds(s string) ([]float64, error) {
	var out []float64
	for _, field := range strings.Split(s, ",") {
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}
		n, err := parseSize(field)
		if err != nil {
			return nil, err
		}
		if len(out) > 0 && n <= out[len(out)-1] {
			return nil, fmt.Errorf("thresholds must ascend: %s", s)
		}
		out = append(out, n)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no thresholds in %q", s)
	}
	if len(out) > 8 {
		return nil, fmt.Errorf("at most 8 thresholds, got %d", len(out))
	}
	return out, nil
}

func parseSize(s string) (float64, error) {
	mult := 1.0
	if len(s) > 0 {
		switch unit := s[len(s)-1]; unit {
		case 'k', 'K':
			mult, s = 1<<10, s[:len(s)-1]
		case 'm', 'M':
			mult, s = 1<<20, s[:len(s)-1]
		case 'g', 'G':
			mult, s = 1<<30, s[:len(s)-1]
		case 'b', 'B':
			s = s[:len(s)-1]
		}
	}
	n, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("bad size %q", s)
	}
	return n * mult, nil
}

// device holds everything drawn for one disk.
type device struct {
	disk bio.Disk

	heat  [nCmds][]float64 // smoothed bytes per second, per bucket
	trail [nCmds][]float64 // "has been here", decaying slowly
	peak  float64          // rolling maximum, for ScaleAuto

	rate [nCmds]float64 // bytes/s, smoothed
	iops [nCmds]float64
	seen [nCmds]int64 // bytes since start
}

func newDevice(d bio.Disk, buckets int) *device {
	dev := &device{disk: d}
	for i := range dev.heat {
		dev.heat[i] = make([]float64, buckets)
		dev.trail[i] = make([]float64, buckets)
	}
	return dev
}

// Model is the bubbletea model.
type Model struct {
	devs  []*device
	index map[string]*device

	frames   <-chan bio.Frame
	errs     <-chan error
	interval time.Duration
	cfg      Config

	width, height int
	paused        bool
	focus         int // index into devs, or -1 for all
	err           error
	warn          string
	frameCount    int
}

type frameMsg bio.Frame
type errMsg struct{ err error }
type closedMsg struct{}

// New builds a model over the given disks, fed by the given channels.
func New(disks []bio.Disk, frames <-chan bio.Frame, errs <-chan error, interval time.Duration, cfg Config) Model {
	def := DefaultConfig()
	if len(cfg.Thresholds) == 0 {
		cfg.Thresholds = def.Thresholds
	}
	if cfg.HeatLife <= 0 {
		cfg.HeatLife = def.HeatLife
	}
	if cfg.Color == ColorAuto {
		cfg.Color = DetectColor()
	}
	// The map writes its own escapes, but the chrome around it goes through
	// lipgloss, which decides on its own what the terminal can do: tell it,
	// so that a forced -color (or a frame piped somewhere) keeps its colors.
	applyColorProfile(cfg.Color)
	if cfg.Buckets <= 0 {
		cfg.Buckets = def.Buckets
	}
	cfg.Buckets = bio.ClampBuckets(cfg.Buckets)
	if cfg.Color == ColorNone {
		cfg.HalfBlocks = false // half blocks need a background color
	}
	m := Model{
		frames:   frames,
		errs:     errs,
		interval: interval,
		cfg:      cfg,
		focus:    -1,
		index:    make(map[string]*device, len(disks)),
		width:    100,
		height:   30,
	}
	for _, d := range disks {
		dev := newDevice(d, cfg.Buckets)
		m.devs = append(m.devs, dev)
		m.index[d.Name] = dev
	}
	return m
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(waitFrame(m.frames), waitErr(m.errs))
}

func waitFrame(ch <-chan bio.Frame) tea.Cmd {
	return func() tea.Msg {
		f, ok := <-ch
		if !ok {
			return closedMsg{}
		}
		return frameMsg(f)
	}
}

func waitErr(ch <-chan error) tea.Cmd {
	return func() tea.Msg {
		err, ok := <-ch
		if !ok || err == nil {
			return nil
		}
		return errMsg{err}
	}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case tea.KeyMsg:
		switch key := msg.String(); key {
		case "q", "ctrl+c", "esc":
			return m, tea.Quit
		case " ", "p":
			m.paused = !m.paused
		case "c":
			m.clear()
		case "0", "a":
			m.focus = -1
		case "s":
			if m.cfg.Scale == ScaleAuto {
				m.cfg.Scale = ScaleFixed
			} else {
				m.cfg.Scale = ScaleAuto
			}
		case "+", "=", "-", "_":
			factor := 2.0
			if key == "-" || key == "_" {
				factor = 0.5
			}
			for i := range m.cfg.Thresholds {
				m.cfg.Thresholds[i] *= factor
			}
			m.cfg.Scale = ScaleFixed
		case "1", "2", "3", "4", "5", "6", "7", "8", "9":
			if n := int(key[0] - '1'); n < len(m.devs) {
				if m.focus == n {
					m.focus = -1
				} else {
					m.focus = n
				}
			}
		}
		return m, nil

	case frameMsg:
		m.Apply(bio.Frame(msg))
		return m, waitFrame(m.frames)

	case errMsg:
		m.err = msg.err
		return m, nil

	case closedMsg:
		if m.err == nil {
			m.err = fmt.Errorf("dtrace stopped")
		}
		return m, nil
	}
	return m, nil
}

// Apply folds one frame of dtrace output into the display state.
func (m *Model) Apply(f bio.Frame) {
	if m.paused {
		return
	}
	m.frameCount++
	seconds := m.interval.Seconds()
	if seconds <= 0 {
		seconds = 0.1
	}

	heatKeep := halfLifeFactor(m.cfg.HeatLife, m.interval)
	trailKeep := halfLifeFactor(m.cfg.TrailLife, m.interval)

	for _, dev := range m.devs {
		for i := 0; i < nCmds; i++ {
			decay(dev.heat[i], heatKeep)
			decay(dev.trail[i], trailKeep)
		}
		dev.peak *= heatKeep
	}

	// heat is a smoothed byte rate per bucket, so a threshold in bytes per
	// second means the same thing whatever the sampling interval is.
	for _, c := range f.Cells {
		dev, ok := m.index[c.Dev]
		if !ok {
			continue
		}
		i, ok := cmdIndex(c.Cmd)
		if !ok || c.Bucket >= len(dev.heat[i]) {
			continue
		}
		dev.heat[i][c.Bucket] += (1 - heatKeep) * float64(c.Bytes) / seconds
		dev.trail[i][c.Bucket] = 1
		if v := dev.heat[i][c.Bucket]; v > dev.peak {
			dev.peak = v
		}
	}

	rates := make(map[string]*[nCmds]float64, len(m.devs))
	ops := make(map[string]*[nCmds]float64, len(m.devs))
	for _, s := range f.Stats {
		dev, ok := m.index[s.Dev]
		if !ok {
			continue
		}
		i, ok := cmdIndex(s.Cmd)
		if !ok {
			continue
		}
		if rates[s.Dev] == nil {
			rates[s.Dev] = &[nCmds]float64{}
			ops[s.Dev] = &[nCmds]float64{}
		}
		rates[s.Dev][i] += float64(s.Bytes) / seconds
		ops[s.Dev][i] += float64(s.Ops) / seconds
		dev.seen[i] += s.Bytes
	}

	const alpha = 0.4
	for _, dev := range m.devs {
		r, o := rates[dev.disk.Name], ops[dev.disk.Name]
		for i := 0; i < nCmds; i++ {
			var rv, ov float64
			if r != nil {
				rv, ov = r[i], o[i]
			}
			dev.rate[i] += alpha * (rv - dev.rate[i])
			dev.iops[i] += alpha * (ov - dev.iops[i])
		}
	}
}

func (m *Model) clear() {
	for _, dev := range m.devs {
		for i := 0; i < nCmds; i++ {
			for j := range dev.trail[i] {
				dev.trail[i][j] = 0
				dev.heat[i][j] = 0
			}
			dev.seen[i] = 0
		}
		dev.peak = 0
	}
}

// halfLifeFactor is what to multiply by each frame for a given half-life;
// a half-life of zero means "do not keep it at all".
func halfLifeFactor(halfLife, interval time.Duration) float64 {
	if halfLife <= 0 {
		return 0
	}
	if interval <= 0 {
		interval = 100 * time.Millisecond
	}
	return math.Pow(0.5, interval.Seconds()/halfLife.Seconds())
}

func decay(v []float64, factor float64) {
	for i := range v {
		if v[i] > 0 {
			if v[i] *= factor; v[i] < 1e-6 {
				v[i] = 0
			}
		}
	}
}

func cmdIndex(c bio.Command) (int, bool) {
	switch c {
	case bio.CmdRead:
		return idxRead, true
	case bio.CmdWrite:
		return idxWrite, true
	case bio.CmdDelete:
		return idxTrim, true
	}
	return 0, false
}

// SetSize is for rendering outside a terminal, as blockio -once does.
func (m *Model) SetSize(w, h int) { m.width, m.height = w, h }

func (m Model) View() string {
	if m.width < 20 || m.height < 8 {
		return "terminal too small"
	}

	head := m.header()
	foot := m.footer()
	body := m.height - lipgloss.Height(head) - lipgloss.Height(foot)
	if body < 3 {
		body = 3
	}

	devs := m.devs
	if m.focus >= 0 && m.focus < len(m.devs) {
		devs = m.devs[m.focus : m.focus+1]
	}
	return strings.Join([]string{head, m.grid(devs, m.width, body), foot}, "\n")
}

// grid lays the panes out in one column for one or two devices, two columns
// otherwise: three devices sit in a 2x2 the way four would.
func (m Model) grid(devs []*device, width, height int) string {
	if len(devs) == 0 {
		return ""
	}
	cols := 2
	if len(devs) <= 2 {
		cols = 1
	}
	rows := (len(devs) + cols - 1) / cols

	paneW := width / cols
	paneH := height / rows

	var lines []string
	for r := 0; r < rows; r++ {
		var row []string
		for c := 0; c < cols; c++ {
			i := r*cols + c
			if i >= len(devs) {
				break
			}
			w := paneW
			if c == cols-1 {
				w = width - paneW*(cols-1) // last column takes the remainder
			}
			h := paneH
			if r == rows-1 {
				h = height - paneH*(rows-1)
			}
			row = append(row, m.pane(devs[i], w, h))
		}
		lines = append(lines, lipgloss.JoinHorizontal(lipgloss.Top, row...))
	}
	return lipgloss.JoinVertical(lipgloss.Left, lines...)
}

// focused reports whether dev is the zoomed device. The grid hands out slice
// positions, which stop matching m.focus as soon as the slice is narrowed to
// the zoomed pane, so match on the device itself.
func (m Model) focused(dev *device) bool {
	return m.focus >= 0 && m.focus < len(m.devs) && m.devs[m.focus] == dev
}

func (m Model) pane(dev *device, width, height int) string {
	inner := width - 2
	innerH := height - 2
	if inner < 4 || innerH < 3 {
		return ""
	}

	style := stylePane
	if m.focused(dev) {
		style = stylePaneFocus
	}

	title := fmt.Sprintf("%s  %s", dev.disk.Name, dev.disk.Size())
	if dev.disk.Descr != "" {
		title += "  " + dev.disk.Descr
	}
	if dev.disk.Rotation > 0 {
		title += fmt.Sprintf("  %d rpm", dev.disk.Rotation)
	}

	body := []string{
		styleDevice.Render(truncate(title, inner)),
		truncate(m.stats(dev), inner),
	}
	body = append(body, renderMap(dev, inner, innerH-len(body), m.cfg)...)

	return style.Width(inner).Height(innerH).Render(strings.Join(body, "\n"))
}

func (m Model) stats(dev *device) string {
	part := func(style lipgloss.Style, label string, i int) string {
		return style.Render(fmt.Sprintf("%s %s/s %s iops",
			label, bio.HumanBytes(int64(dev.rate[i])), shortNum(dev.iops[i])))
	}
	out := part(styleRead, "R", idxRead) + "  " + part(styleWrite, "W", idxWrite)
	if dev.rate[idxTrim] > 0 || dev.seen[idxTrim] > 0 {
		out += "  " + part(styleTrim, "T", idxTrim)
	}
	total := dev.seen[idxRead] + dev.seen[idxWrite]
	return out + styleMuted.Render(fmt.Sprintf("   %s total", bio.HumanBytes(total)))
}

// renderMap draws the device: every cell is a slice of the address space,
// left to right and top to bottom, LBA 0 first.  With half blocks each
// terminal row carries two rows of cells, the top half in the foreground
// color and the bottom half in the background one.
func renderMap(dev *device, width, rows int, cfg Config) []string {
	if rows < 1 || width < 1 {
		return nil
	}

	perRow := 1
	if cfg.HalfBlocks {
		perRow = 2
	}
	cells := width * rows * perRow
	buckets := len(dev.heat[idxRead])

	at := func(cell int) paint {
		lo := cell * buckets / cells
		hi := (cell + 1) * buckets / cells
		if hi <= lo {
			hi = lo + 1
		}
		if hi > buckets {
			hi = buckets
		}
		cmd, heat, active := cellHeat(dev, lo, hi, cfg)
		return paintOf(cfg.Color, cmd, heat, active)
	}

	out := make([]string, 0, rows)
	var line strings.Builder
	for r := 0; r < rows; r++ {
		line.Reset()
		// Foreground and background are tracked apart, so a run of cells
		// over the same background only pays for the halves that change.
		lastFG, lastBG := "", ""
		for c := 0; c < width; c++ {
			if !cfg.HalfBlocks {
				p := at(r*width + c)
				if esc := p.fg(); esc != lastFG {
					line.WriteString(esc)
					lastFG = esc
				}
				if p.kind == paintMono {
					line.WriteString(p.mono)
				} else {
					line.WriteString(blockFull)
				}
				continue
			}
			top := at((2*r)*width + c)
			bottom := at((2*r+1)*width + c)
			if esc := top.fg(); esc != lastFG {
				line.WriteString(esc)
				lastFG = esc
			}
			if esc := bottom.bg(); esc != lastBG {
				line.WriteString(esc)
				lastBG = esc
			}
			line.WriteString(blockUpper)
		}
		if lastFG != "" || lastBG != "" {
			line.WriteString(escReset)
		}
		out = append(out, line.String())
	}
	return out
}

// cellHeat picks the loudest command in the cell's bucket range and turns
// its byte rate into a 0..1 position on that command's ramp.  A cell with
// no current activity reports its trail instead.
func cellHeat(dev *device, lo, hi int, cfg Config) (cmd int, heat float64, active bool) {
	var rate [nCmds]float64
	var trail [nCmds]float64
	for i := 0; i < nCmds; i++ {
		for b := lo; b < hi; b++ {
			if v := dev.heat[i][b]; v > rate[i] {
				rate[i] = v
			}
			if v := dev.trail[i][b]; v > trail[i] {
				trail[i] = v
			}
		}
	}

	best, bestVal := -1, 0.0
	for i := 0; i < nCmds; i++ {
		if rate[i] > bestVal {
			best, bestVal = i, rate[i]
		}
	}
	if best >= 0 && bestVal > 0 {
		return best, heatLevel(bestVal, dev.peak, cfg), true
	}

	best, bestVal = -1, trailFloor
	for i := 0; i < nCmds; i++ {
		if trail[i] > bestVal {
			best, bestVal = i, trail[i]
		}
	}
	if best >= 0 {
		return best, 0, false
	}
	return -1, 0, false
}

// heatLevel places a byte rate on the 0..1 ramp, either against the
// configured thresholds or against the device's own busiest cell.
func heatLevel(rate, peak float64, cfg Config) float64 {
	if cfg.Scale == ScaleFixed && len(cfg.Thresholds) > 0 {
		step := 0
		for _, t := range cfg.Thresholds {
			if rate < t {
				break
			}
			step++
		}
		return float64(step) / float64(len(cfg.Thresholds))
	}
	if peak <= 0 {
		peak = rate
	}
	// Square root: a trickle of metadata should still be visible next to a
	// resilver saturating the disk.
	return math.Min(1, math.Sqrt(rate/peak))
}

func (m Model) header() string {
	title := styleTitle.Render("blockio")
	what := fmt.Sprintf(" %d device", len(m.devs))
	if len(m.devs) != 1 {
		what += "s"
	}
	if m.focus >= 0 && m.focus < len(m.devs) {
		what += ", showing " + m.devs[m.focus].disk.Name
	}
	line := title + styleMuted.Render(what)
	line += styleMuted.Render("  scale " + m.scaleLabel())
	if m.paused {
		line += "  " + stylePause.Render("PAUSED")
	}
	if m.err != nil {
		line += "  " + styleErr.Render(truncate(m.err.Error(), m.width-lipgloss.Width(line)-2))
	}
	return line
}

// scaleLabel says what the colors are measured against.
func (m Model) scaleLabel() string {
	if m.cfg.Scale == ScaleAuto {
		return "auto"
	}
	steps := make([]string, 0, len(m.cfg.Thresholds))
	for _, t := range m.cfg.Thresholds {
		steps = append(steps, bio.HumanBytes(int64(t)))
	}
	return strings.Join(steps, "/") + "/s"
}

func (m Model) footer() string {
	legend := styleRead.Render("█ read") + "  " +
		styleWrite.Render("█ write") + "  " +
		styleTrim.Render("█ trim")
	keys := styleMuted.Render("space pause · c clear · s scale · +/- thresholds · 1-9 one · 0 all · q quit")
	gap := m.width - lipgloss.Width(legend) - lipgloss.Width(keys)
	if gap < 2 {
		return legend
	}
	return legend + strings.Repeat(" ", gap) + keys
}

func shortNum(v float64) string {
	switch {
	case v >= 100000:
		return fmt.Sprintf("%.0fk", v/1000)
	case v >= 10000:
		return fmt.Sprintf("%.1fk", v/1000)
	case v >= 10 || v == 0:
		return fmt.Sprintf("%.0f", v)
	default:
		return fmt.Sprintf("%.1f", v)
	}
}

func truncate(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= width {
		return s
	}
	r := []rune(s)
	if width == 1 {
		return "…"
	}
	return string(r[:width-1]) + "…"
}
