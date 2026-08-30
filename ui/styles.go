package ui

import "github.com/charmbracelet/lipgloss"

// Reads are green, writes red, TRIM amber: five steps of brightness each,
// plus a dim step for "touched a while ago" and a dark step for idle.
var (
	rampRead  = [...]int{28, 34, 40, 46, 82}
	rampWrite = [...]int{88, 124, 160, 196, 203}
	rampTrim  = [...]int{94, 136, 178, 214, 220}

	trailColor = [...]int{22, 52, 58}
	idleColor  = 236
)

const (
	blockFull = "█"
	blockIdle = "█"
)

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
