// Package ui draws block device activity as a defrag-style map: one cell
// per slice of the device, green for reads, red for writes, amber for TRIM.
package ui

import (
	"fmt"
	"math"
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

const (
	heatDecay  = 0.55  // per frame: activity fades over about half a second
	trailDecay = 0.997 // per frame: where the head has been, for minutes
	trailFloor = 0.04
	levels     = 5
)

// device holds everything drawn for one disk.
type device struct {
	disk bio.Disk

	heat  [nCmds][]float64 // recent bytes per bucket, decaying
	trail [nCmds][]float64 // "has been here", decaying slowly
	peak  float64          // rolling maximum, so the ramp is self-scaling

	rate [nCmds]float64 // bytes/s, smoothed
	iops [nCmds]float64
	seen [nCmds]int64 // bytes since start
}

func newDevice(d bio.Disk) *device {
	dev := &device{disk: d}
	for i := range dev.heat {
		dev.heat[i] = make([]float64, bio.Buckets)
		dev.trail[i] = make([]float64, bio.Buckets)
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
func New(disks []bio.Disk, frames <-chan bio.Frame, errs <-chan error, interval time.Duration) Model {
	m := Model{
		frames:   frames,
		errs:     errs,
		interval: interval,
		focus:    -1,
		index:    make(map[string]*device, len(disks)),
		width:    100,
		height:   30,
	}
	for _, d := range disks {
		dev := newDevice(d)
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

	for _, dev := range m.devs {
		for i := 0; i < nCmds; i++ {
			decay(dev.heat[i], heatDecay)
			decay(dev.trail[i], trailDecay)
		}
		dev.peak *= 0.98
	}

	for _, c := range f.Cells {
		dev, ok := m.index[c.Dev]
		if !ok {
			continue
		}
		i, ok := cmdIndex(c.Cmd)
		if !ok || c.Bucket >= len(dev.heat[i]) {
			continue
		}
		dev.heat[i][c.Bucket] += float64(c.Bytes)
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
			row = append(row, m.pane(devs[i], i, w, h))
		}
		lines = append(lines, lipgloss.JoinHorizontal(lipgloss.Top, row...))
	}
	return lipgloss.JoinVertical(lipgloss.Left, lines...)
}

func (m Model) pane(dev *device, i, width, height int) string {
	inner := width - 2
	innerH := height - 2
	if inner < 4 || innerH < 3 {
		return ""
	}

	style := stylePane
	if m.focus == i {
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
	body = append(body, renderMap(dev, inner, innerH-len(body))...)

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
// left to right and top to bottom, LBA 0 first.
func renderMap(dev *device, width, rows int) []string {
	if rows < 1 || width < 1 {
		return nil
	}
	cells := width * rows

	out := make([]string, 0, rows)
	var line strings.Builder
	for r := 0; r < rows; r++ {
		line.Reset()
		last := -1
		for c := 0; c < width; c++ {
			cell := r*width + c
			lo := cell * bio.Buckets / cells
			hi := (cell + 1) * bio.Buckets / cells
			if hi <= lo {
				hi = lo + 1
			}
			if hi > bio.Buckets {
				hi = bio.Buckets
			}

			color := cellColor(dev, lo, hi)
			if color != last {
				fmt.Fprintf(&line, "\x1b[38;5;%dm", color)
				last = color
			}
			line.WriteString(blockFull)
		}
		line.WriteString("\x1b[0m")
		out = append(out, line.String())
	}
	return out
}

// cellColor picks the loudest command in the cell's bucket range and turns
// its share of the device's rolling peak into a step on that ramp.
func cellColor(dev *device, lo, hi int) int {
	var heat [nCmds]float64
	var trail [nCmds]float64
	for i := 0; i < nCmds; i++ {
		for b := lo; b < hi; b++ {
			if v := dev.heat[i][b]; v > heat[i] {
				heat[i] = v
			}
			if v := dev.trail[i][b]; v > trail[i] {
				trail[i] = v
			}
		}
	}

	best, bestVal := -1, 0.0
	for i := 0; i < nCmds; i++ {
		if heat[i] > bestVal {
			best, bestVal = i, heat[i]
		}
	}
	if best >= 0 && bestVal > 0 {
		peak := dev.peak
		if peak <= 0 {
			peak = bestVal
		}
		// Square root: a trickle of metadata should still be visible next
		// to a resilver saturating the disk.
		step := int(math.Sqrt(bestVal/peak)*float64(levels-1) + 0.5)
		if step < 0 {
			step = 0
		}
		if step >= levels {
			step = levels - 1
		}
		switch best {
		case idxRead:
			return rampRead[step]
		case idxWrite:
			return rampWrite[step]
		default:
			return rampTrim[step]
		}
	}

	best, bestVal = -1, trailFloor
	for i := 0; i < nCmds; i++ {
		if trail[i] > bestVal {
			best, bestVal = i, trail[i]
		}
	}
	if best >= 0 {
		return trailColor[best]
	}
	return idleColor
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
	if m.paused {
		line += "  " + stylePause.Render("PAUSED")
	}
	if m.err != nil {
		line += "  " + styleErr.Render(truncate(m.err.Error(), m.width-lipgloss.Width(line)-2))
	}
	return line
}

func (m Model) footer() string {
	legend := styleRead.Render("█ read") + "  " +
		styleWrite.Render("█ write") + "  " +
		styleTrim.Render("█ trim") + "  " +
		styleMuted.Render("█ idle")
	keys := styleMuted.Render("space pause · c clear · 1-9 one device · 0 all · q quit")
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
