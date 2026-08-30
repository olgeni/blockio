package ui

import (
	"fmt"
	"math"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// ColorMode is how much color the map may use.  Truecolor gives the decay a
// continuous gradient; 256 colors step through a ramp; 16 colors manage dim
// and bright; without color the cells fall back to block characters.
type ColorMode int

const (
	ColorAuto ColorMode = iota
	ColorTrue
	Color256
	Color16
	ColorNone
)

func (c ColorMode) String() string {
	switch c {
	case ColorTrue:
		return "truecolor"
	case Color256:
		return "256"
	case Color16:
		return "16"
	case ColorNone:
		return "off"
	}
	return "auto"
}

// ParseColorMode reads the -color flag or the config file's color option.
func ParseColorMode(s string) (ColorMode, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "auto", "":
		return ColorAuto, nil
	case "truecolor", "24bit", "rgb":
		return ColorTrue, nil
	case "256", "8bit":
		return Color256, nil
	case "16", "ansi", "basic":
		return Color16, nil
	case "off", "none", "mono":
		return ColorNone, nil
	}
	return ColorAuto, fmt.Errorf("bad color mode %q (auto, truecolor, 256, 16, off)", s)
}

// DetectColor asks the terminal what it can do.
func DetectColor() ColorMode {
	if os.Getenv("NO_COLOR") != "" {
		return ColorNone
	}
	switch termenv.EnvColorProfile() {
	case termenv.TrueColor:
		return ColorTrue
	case termenv.ANSI256:
		return Color256
	case termenv.ANSI:
		return Color16
	default:
		return ColorNone
	}
}

// applyColorProfile points lipgloss at the depth the map is drawn in.
func applyColorProfile(mode ColorMode) {
	switch mode {
	case ColorTrue:
		lipgloss.SetColorProfile(termenv.TrueColor)
	case Color256:
		lipgloss.SetColorProfile(termenv.ANSI256)
	case Color16:
		lipgloss.SetColorProfile(termenv.ANSI)
	case ColorNone:
		lipgloss.SetColorProfile(termenv.Ascii)
	}
}

// The three tracked commands, coldest to hottest.  The 256-color ramps are
// picked from the cube; truecolor interpolates between the endpoints.
var (
	ramp256 = [nCmds][5]int{
		idxRead:  {28, 34, 40, 46, 82},
		idxWrite: {88, 124, 160, 196, 203},
		idxTrim:  {94, 136, 178, 214, 220},
	}
	trail256 = [nCmds]int{idxRead: 22, idxWrite: 52, idxTrim: 58}
	idle256  = 236

	rampRGB = [nCmds][2][3]float64{
		idxRead:  {{6, 40, 14}, {96, 255, 118}},
		idxWrite: {{48, 8, 8}, {255, 96, 84}},
		idxTrim:  {{48, 34, 4}, {255, 202, 64}},
	}
	idleRGB = [3]float64{32, 32, 32}

	ansi16 = [nCmds][2]int{idxRead: {32, 92}, idxWrite: {31, 91}, idxTrim: {33, 93}}
	// Black rather than bright black: with half blocks the idle cells are
	// most of the map, and a gray that heavy drowns out the activity.
	ansi16Idle = 30

	// Without color, density stands in for brightness.
	monoRamp = [...]string{"░", "▒", "▓", "█"}
)

const (
	blockFull  = "█"
	blockUpper = "▀" // foreground is the top half, background the bottom
	escReset   = "\x1b[0m"
)

// paint is one cell's color, in whichever form the terminal takes it.  The
// kind is carried explicitly: a 256-color index and an ANSI code overlap in
// value (82 is a green in the cube and bright green in ANSI), so there is
// nothing in the number itself to tell them apart.
type paintKind int

const (
	paintMono paintKind = iota
	paintRGB
	paint256
	paintANSI
)

type paint struct {
	kind paintKind
	rgb  [3]float64
	code int
	mono string
}

func (p paint) fg() string {
	switch p.kind {
	case paintRGB:
		return fmt.Sprintf("\x1b[38;2;%d;%d;%dm", int(p.rgb[0]+0.5), int(p.rgb[1]+0.5), int(p.rgb[2]+0.5))
	case paint256:
		return fmt.Sprintf("\x1b[38;5;%dm", p.code)
	case paintANSI:
		return fmt.Sprintf("\x1b[%dm", p.code)
	}
	return ""
}

func (p paint) bg() string {
	switch p.kind {
	case paintRGB:
		return fmt.Sprintf("\x1b[48;2;%d;%d;%dm", int(p.rgb[0]+0.5), int(p.rgb[1]+0.5), int(p.rgb[2]+0.5))
	case paint256:
		return fmt.Sprintf("\x1b[48;5;%dm", p.code)
	case paintANSI:
		return fmt.Sprintf("\x1b[%dm", p.code+10)
	}
	return ""
}

// paintOf colors one cell: cmd is the command that owns it (negative for an
// idle cell), heat runs 0 to 1, and a cell with no current activity is
// drawn as a trail.
func paintOf(mode ColorMode, cmd int, heat float64, active bool) paint {
	if mode == ColorNone {
		switch {
		case cmd < 0:
			return paint{kind: paintMono, mono: " "}
		case !active:
			return paint{kind: paintMono, mono: "·"}
		default:
			step := clamp(int(heat*float64(len(monoRamp)-1)+0.5), 0, len(monoRamp)-1)
			return paint{kind: paintMono, mono: monoRamp[step]}
		}
	}

	switch {
	case cmd < 0:
		switch mode {
		case ColorTrue:
			return paint{kind: paintRGB, rgb: idleRGB}
		case Color16:
			return paint{kind: paintANSI, code: ansi16Idle}
		default:
			return paint{kind: paint256, code: idle256}
		}
	case !active:
		switch mode {
		case ColorTrue:
			return paint{kind: paintRGB, rgb: mix(idleRGB, rampRGB[cmd][0], 0.75)}
		case Color16:
			return paint{kind: paintANSI, code: ansi16[cmd][0]}
		default:
			return paint{kind: paint256, code: trail256[cmd]}
		}
	default:
		switch mode {
		case ColorTrue:
			return paint{kind: paintRGB, rgb: mix(rampRGB[cmd][0], rampRGB[cmd][1], heat)}
		case Color16:
			i := 0
			if heat >= 0.5 {
				i = 1
			}
			return paint{kind: paintANSI, code: ansi16[cmd][i]}
		default:
			steps := len(ramp256[cmd])
			step := clamp(int(heat*float64(steps-1)+0.5), 0, steps-1)
			return paint{kind: paint256, code: ramp256[cmd][step]}
		}
	}
}

// mix interpolates in gamma space, which keeps the dim end of the gradient
// from washing out.
func mix(from, to [3]float64, t float64) [3]float64 {
	t = math.Max(0, math.Min(1, t))
	var out [3]float64
	for i := range out {
		a := math.Pow(from[i]/255, 2.2)
		b := math.Pow(to[i]/255, 2.2)
		out[i] = math.Pow(a+(b-a)*t, 1/2.2) * 255
	}
	return out
}

func rgbEscape(c [3]float64) string {
	return fmt.Sprintf("\x1b[38;2;%d;%d;%dm", int(c[0]+0.5), int(c[1]+0.5), int(c[2]+0.5))
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

var (
	colAccent = lipgloss.AdaptiveColor{Light: "#005f87", Dark: "#5fd7ff"}
	colMuted  = lipgloss.AdaptiveColor{Light: "#6c6c6c", Dark: "#8a8a8a"}

	styleTitle = lipgloss.NewStyle().Bold(true).
			Foreground(lipgloss.Color("#ffffff")).
			Background(lipgloss.AdaptiveColor{Light: "#005f87", Dark: "#1f3f5f"}).
			Padding(0, 1)
	styleDevice = lipgloss.NewStyle().Bold(true).Foreground(colAccent)
	styleMuted  = lipgloss.NewStyle().Foreground(colMuted)
	styleRead   = lipgloss.NewStyle().Foreground(lipgloss.Color("40"))
	styleWrite  = lipgloss.NewStyle().Foreground(lipgloss.Color("160"))
	styleTrim   = lipgloss.NewStyle().Foreground(lipgloss.Color("178"))
	styleErr    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("196"))
	stylePause  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("220"))

	stylePane = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.AdaptiveColor{Light: "#b0b0b0", Dark: "#3a3a3a"})
	stylePaneFocus = stylePane.BorderForeground(colAccent)
)
