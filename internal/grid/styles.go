package grid

import (
	"fmt"
	"strconv"

	"github.com/charmbracelet/lipgloss"
)

var (
	titleStyle = lipgloss.NewStyle().Bold(true).Padding(0, 1)

	headerStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("245")).
			Padding(0, 1)

	selectedStyle = lipgloss.NewStyle().
			Background(lipgloss.Color("236")).
			Padding(0, 1)

	normalStyle = lipgloss.NewStyle().Padding(0, 1)

	statusActive = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#a6e3a1")).
			SetString("●")

	statusIdle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#f9e2af")).
			SetString("○")

	statusMinimized = lipgloss.NewStyle().
			Foreground(lipgloss.Color("245")).
			SetString("─")

	statusDisconnected = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#f38ba8")).
				SetString("✕")

	helpStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("241")).
			Padding(1, 0)
)

// hexColor converts a hex string to a lipgloss color.
func hexColor(hex string) lipgloss.Color {
	if hex == "" {
		return lipgloss.Color("7")
	}
	return lipgloss.Color("#" + hex)
}

// coloredHost renders a hostname in its assigned color.
func coloredHost(name, hex string) string {
	if hex == "" {
		return name
	}
	return lipgloss.NewStyle().Foreground(hexColor(hex)).Render(name)
}

// autoContrast returns black or white foreground based on background luminance.
func autoContrast(hex string) string {
	if len(hex) != 6 {
		return "#f0f0f0"
	}
	r, _ := strconv.ParseUint(hex[0:2], 16, 8)
	g, _ := strconv.ParseUint(hex[2:4], 16, 8)
	b, _ := strconv.ParseUint(hex[4:6], 16, 8)
	luminance := (r*299 + g*587 + b*114) / 1000
	if luminance > 128 {
		return "#1e1e2e"
	}
	return "#f0f0f0"
}

func statusIcon(status string) string {
	switch status {
	case "active":
		return statusActive.String()
	case "idle":
		return statusIdle.String()
	case "minimized":
		return statusMinimized.String()
	case "disconnected":
		return statusDisconnected.String()
	default:
		return fmt.Sprintf("%s", status)
	}
}
