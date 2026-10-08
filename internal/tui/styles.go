package tui

import (
	"charm.land/lipgloss/v2"

	"github.com/k0nsta/rigger/internal/action"
)

var (
	colorAccent = lipgloss.Color("#7aa2f7")
	colorOK     = lipgloss.Color("#9ece6a")
	colorTodo   = lipgloss.Color("#e0af68")
	colorBad    = lipgloss.Color("#f7768e")
	colorDim    = lipgloss.Color("#737aa2")

	titleStyle    = lipgloss.NewStyle().Bold(true).Foreground(colorAccent)
	headerStyle   = lipgloss.NewStyle().Bold(true)
	dimStyle      = lipgloss.NewStyle().Foreground(colorDim)
	okStyle       = lipgloss.NewStyle().Foreground(colorOK)
	todoStyle     = lipgloss.NewStyle().Foreground(colorTodo)
	badStyle      = lipgloss.NewStyle().Foreground(colorBad)
	cursorStyle   = lipgloss.NewStyle().Bold(true).Foreground(colorAccent)
	helpStyle     = lipgloss.NewStyle().Foreground(colorDim)
	selectedStyle = lipgloss.NewStyle().Bold(true)
)

// statusStyle colours a status by how much it needs.
func statusStyle(s action.Status) lipgloss.Style {
	switch {
	case s.Settled() || s == action.NoSnapshot:
		return okStyle
	case s == action.NeedsSecret || s == action.Differs || s == action.BothChanged:
		return badStyle
	default:
		return todoStyle
	}
}
