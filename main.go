// Kiroku — a minimal TUI task manager built with Bubble Tea.
//
// Features:
//   - Spaces: named task lists you slide between with shift+left/right
//   - Checkbox-style task list with vim-style navigation (j/k)
//   - Toggle tasks done/undone with space or enter
//   - Add tasks via a huh-powered input dialog (a)
//   - Delete single (backspace) or all tasks (ctrl+d) with confirmation
//   - Reorder tasks with shift+up/down
//   - Progress bar showing completion ratio
//   - Completed tasks automatically sort to the bottom
//   - Persistence to ~/.kikoru/data.json
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
)

type Task struct {
	ID    int    `json:"id"`
	Title string `json:"title"`
	Done  bool   `json:"done"`
}

type Space struct {
	Name   string `json:"name"`
	Tasks  []Task `json:"tasks"`
	NextID int    `json:"next_id"`
}

type AppData struct {
	Spaces    []Space `json:"spaces"`
	ActiveIdx int     `json:"active_idx"`
}

// Model is the top-level Bubble Tea model.
type Model struct {
	data     AppData
	dataPath string

	focusCol    int // 0 = todo (left), 1 = done (right)
	selectedIdx int // index within the focused column's filtered list
	width       int
	height      int
	mode        string // "normal", "add", "add_space", "rename_space", "delete_confirm", "delete_all_confirm", "delete_space_confirm", "help"

	form *huh.Form
}

// Persist
func dataFilePath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".kikoru", "data.json")
}

func loadData(path string) (AppData, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return AppData{}, err
	}
	var d AppData
	if err := json.Unmarshal(b, &d); err != nil {
		return AppData{}, err
	}
	return d, nil
}

func saveData(path string, d AppData) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0644)
}

func (m Model) save() {
	_ = saveData(m.dataPath, m.data)
}

// activeSpace returns a pointer to the current space, or nil if none exist.
func (m *Model) activeSpace() *Space {
	if len(m.data.Spaces) == 0 {
		return nil
	}
	if m.data.ActiveIdx >= len(m.data.Spaces) {
		m.data.ActiveIdx = len(m.data.Spaces) - 1
	}
	return &m.data.Spaces[m.data.ActiveIdx]
}

// todoIndices returns the indices into sp.Tasks for incomplete tasks.
func todoIndices(sp *Space) []int {
	var out []int
	if sp == nil {
		return out
	}
	for i, t := range sp.Tasks {
		if !t.Done {
			out = append(out, i)
		}
	}
	return out
}

// doneIndices returns the indices into sp.Tasks for completed tasks.
func doneIndices(sp *Space) []int {
	var out []int
	if sp == nil {
		return out
	}
	for i, t := range sp.Tasks {
		if t.Done {
			out = append(out, i)
		}
	}
	return out
}

// selectedRealIdx maps focusCol + selectedIdx to the real index in sp.Tasks.
// Returns -1 if nothing is selected.
func (m *Model) selectedRealIdx() int {
	sp := m.activeSpace()
	if sp == nil {
		return -1
	}
	var indices []int
	if m.focusCol == 0 {
		indices = todoIndices(sp)
	} else {
		indices = doneIndices(sp)
	}
	if len(indices) == 0 || m.selectedIdx >= len(indices) {
		return -1
	}
	return indices[m.selectedIdx]
}

// clampSelection ensures selectedIdx is valid for the focused column.
func (m *Model) clampSelection() {
	sp := m.activeSpace()
	if sp == nil {
		m.selectedIdx = 0
		return
	}
	var n int
	if m.focusCol == 0 {
		n = len(todoIndices(sp))
	} else {
		n = len(doneIndices(sp))
	}
	if n == 0 {
		m.selectedIdx = 0
	} else if m.selectedIdx >= n {
		m.selectedIdx = n - 1
	}
}

// Color palette and styles

var (
	colorBg   = lipgloss.Color("#2b2539")
	colorText = lipgloss.Color("#d5ccdf")
	colorDim  = lipgloss.Color("#7a7088")

	colorRose   = lipgloss.Color("#d4a0a0")
	colorPeach  = lipgloss.Color("#e8b490")
	colorLavend = lipgloss.Color("#c4a8e0")

	colorDanger = lipgloss.Color("#cf7676")

	styleTitle = lipgloss.NewStyle().
			Foreground(colorLavend).
			Bold(true).
			MarginBottom(0)

	styleSelected = lipgloss.NewStyle().
			Foreground(colorBg).
			Background(colorPeach).
			Padding(0, 1)

	styleUnselected = lipgloss.NewStyle().
			Foreground(colorText).
			Padding(0, 1)

	styleBorder = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorDim).
			Padding(1)

	styleHelp = lipgloss.NewStyle().
			Foreground(colorDim).
			Italic(true)

	styleMuted = lipgloss.NewStyle().
			Foreground(colorDim)

	styleDone = lipgloss.NewStyle().
			Foreground(colorDim).
			Strikethrough(true).
			Padding(0, 1)

	styleDoneSelected = lipgloss.NewStyle().
				Foreground(colorBg).
				Background(colorDim).
				Padding(0, 1)

	styleDialogBox = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorLavend).
			Padding(1, 2).
			Width(50)
)

func buildTheme() *huh.Theme {
	t := huh.ThemeBase()

	t.Focused.Base = lipgloss.NewStyle().
		PaddingLeft(1).
		BorderStyle(lipgloss.ThickBorder()).
		BorderLeft(true).
		BorderForeground(colorRose)
	t.Focused.Title = lipgloss.NewStyle().
		Foreground(colorPeach).
		Bold(true)
	t.Focused.Description = lipgloss.NewStyle().
		Foreground(colorDim)
	t.Focused.ErrorIndicator = lipgloss.NewStyle().
		Foreground(colorDanger).
		SetString(" *")
	t.Focused.ErrorMessage = lipgloss.NewStyle().
		Foreground(colorDanger)
	t.Focused.TextInput.Cursor = lipgloss.NewStyle().
		Foreground(colorPeach)
	t.Focused.TextInput.CursorText = lipgloss.NewStyle().
		Foreground(colorText)
	t.Focused.TextInput.Placeholder = lipgloss.NewStyle().
		Foreground(colorDim)
	t.Focused.TextInput.Prompt = lipgloss.NewStyle().
		Foreground(colorLavend)
	t.Focused.TextInput.Text = lipgloss.NewStyle().
		Foreground(colorText)
	t.Focused.FocusedButton = lipgloss.NewStyle().
		Foreground(colorBg).
		Background(colorPeach).
		Padding(0, 2).
		Bold(true)
	t.Focused.BlurredButton = lipgloss.NewStyle().
		Foreground(colorDim).
		Padding(0, 2)

	t.Blurred.Base = lipgloss.NewStyle().
		PaddingLeft(1).
		BorderStyle(lipgloss.HiddenBorder()).
		BorderLeft(true)
	t.Blurred.Title = t.Focused.Title
	t.Blurred.Description = t.Focused.Description
	t.Blurred.TextInput = t.Focused.TextInput
	t.Blurred.FocusedButton = t.Focused.FocusedButton
	t.Blurred.BlurredButton = t.Focused.BlurredButton

	return t
}

func buildDangerTheme() *huh.Theme {
	t := buildTheme()
	t.Focused.Base = t.Focused.Base.BorderForeground(colorDanger)
	t.Focused.Title = t.Focused.Title.Foreground(colorDanger)
	t.Focused.FocusedButton = lipgloss.NewStyle().
		Foreground(colorText).
		Background(colorDanger).
		Padding(0, 2).
		Bold(true)
	return t
}

// Interface
// initSpaceMsg is sent on startup when no spaces exist to trigger the dialog.
type initSpaceMsg struct{}

func (m Model) Init() tea.Cmd {
	if len(m.data.Spaces) == 0 {
		return func() tea.Msg { return initSpaceMsg{} }
	}
	return nil
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// Help mode: any key dismisses it.
	if m.mode == "help" {
		if _, ok := msg.(tea.KeyMsg); ok {
			m.mode = "normal"
			return m, nil
		}
		if wsm, ok := msg.(tea.WindowSizeMsg); ok {
			m.width = wsm.Width
			m.height = wsm.Height
		}
		return m, nil
	}

	// Route to form when active.
	if m.form != nil {
		if keyMsg, ok := msg.(tea.KeyMsg); ok && keyMsg.String() == "esc" {
			// Don't allow cancelling the initial space creation if there are no spaces.
			if m.mode == "add_space" && len(m.data.Spaces) == 0 {
				return m, nil
			}
			m.form = nil
			m.mode = "normal"
			return m, nil
		}

		form, cmd := m.form.Update(msg)
		if f, ok := form.(*huh.Form); ok {
			m.form = f
		}

		if m.form.State == huh.StateCompleted {
			return m.handleFormComplete()
		}
		if m.form.State == huh.StateAborted {
			if m.mode == "add_space" && len(m.data.Spaces) == 0 {
				return m, nil
			}
			m.form = nil
			m.mode = "normal"
			return m, nil
		}

		if wsm, ok := msg.(tea.WindowSizeMsg); ok {
			m.width = wsm.Width
			m.height = wsm.Height
		}

		return m, cmd
	}

	switch msg := msg.(type) {
	case initSpaceMsg:
		m.mode = "add_space"
		m.form = huh.NewForm(
			huh.NewGroup(
				huh.NewInput().
					Key("name").
					Title("Create your first space").
					Placeholder("e.g. work, personal, ideas"),
			),
		).WithTheme(buildTheme()).WithWidth(44).WithShowHelp(false)
		return m, m.form.Init()

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit
		case "q":
			if m.mode == "normal" {
				return m, tea.Quit
			}
		case "?":
			if m.mode == "normal" {
				m.mode = "help"
				return m, nil
			}
		}

		switch m.mode {
		case "normal":
			return m.handleNormalMode(msg)
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
	}

	return m, nil
}

func (m Model) handleFormComplete() (tea.Model, tea.Cmd) {
	switch m.mode {
	case "add":
		title := m.form.GetString("title")
		if title != "" {
			sp := m.activeSpace()
			if sp != nil {
				sp.Tasks = append(sp.Tasks, Task{
					ID:    sp.NextID,
					Title: title,
				})
				sp.NextID++
				m.focusCol = 0
				m.selectedIdx = len(todoIndices(sp)) - 1
				m.save()
			}
		}

	case "add_space":
		name := m.form.GetString("name")
		if name != "" {
			m.data.Spaces = append(m.data.Spaces, Space{
				Name:   name,
				NextID: 1,
			})
			m.data.ActiveIdx = len(m.data.Spaces) - 1
			m.focusCol = 0
			m.selectedIdx = 0
			m.save()
		}

	case "delete_confirm":
		if m.form.GetBool("confirm") {
			sp := m.activeSpace()
			realIdx := m.selectedRealIdx()
			if sp != nil && realIdx >= 0 {
				sp.Tasks = slices.Delete(sp.Tasks, realIdx, realIdx+1)
				m.clampSelection()
				m.save()
			}
		}

	case "delete_all_confirm":
		if m.form.GetBool("confirm") {
			sp := m.activeSpace()
			if sp != nil {
				sp.Tasks = nil
				m.focusCol = 0
				m.selectedIdx = 0
				m.save()
			}
		}

	case "delete_space_confirm":
		if m.form.GetBool("confirm") {
			if len(m.data.Spaces) > 0 {
				m.data.Spaces = slices.Delete(m.data.Spaces, m.data.ActiveIdx, m.data.ActiveIdx+1)
				if m.data.ActiveIdx >= len(m.data.Spaces) && m.data.ActiveIdx > 0 {
					m.data.ActiveIdx--
				}
				m.focusCol = 0
				m.selectedIdx = 0
				m.save()
			}
		}

	case "rename_space":
		name := m.form.GetString("name")
		if name != "" {
			sp := m.activeSpace()
			if sp != nil {
				sp.Name = name
				m.save()
			}
		}
	}

	m.form = nil
	m.mode = "normal"
	return m, nil
}

func (m Model) handleNormalMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	sp := m.activeSpace()

	switch msg.String() {
	case "tab":
		if sp == nil {
			return m, nil
		}
		if m.focusCol == 0 {
			if len(doneIndices(sp)) > 0 {
				m.focusCol = 1
				m.clampSelection()
			}
		} else {
			m.focusCol = 0
			m.clampSelection()
		}
		return m, nil

	case "shift+right":
		if len(m.data.Spaces) == 0 || m.data.ActiveIdx >= len(m.data.Spaces)-1 {
			m.mode = "add_space"
			m.form = huh.NewForm(
				huh.NewGroup(
					huh.NewInput().
						Key("name").
						Title("New Space").
						Placeholder("e.g. work, personal, ideas"),
				),
			).WithTheme(buildTheme()).WithWidth(44).WithShowHelp(false)
			return m, m.form.Init()
		}
		m.data.ActiveIdx++
		m.focusCol = 0
		m.selectedIdx = 0
		m.save()
		return m, nil

	case "shift+left":
		if m.data.ActiveIdx > 0 {
			m.data.ActiveIdx--
			m.focusCol = 0
			m.selectedIdx = 0
			m.save()
		}
		return m, nil

	case "up", "k":
		if sp != nil {
			var n int
			if m.focusCol == 0 {
				n = len(todoIndices(sp))
			} else {
				n = len(doneIndices(sp))
			}
			if n > 0 {
				m.selectedIdx = (m.selectedIdx - 1 + n) % n
			}
		}
		return m, nil

	case "down", "j":
		if sp != nil {
			var n int
			if m.focusCol == 0 {
				n = len(todoIndices(sp))
			} else {
				n = len(doneIndices(sp))
			}
			if n > 0 {
				m.selectedIdx = (m.selectedIdx + 1) % n
			}
		}
		return m, nil

	case " ", "enter":
		realIdx := m.selectedRealIdx()
		if sp != nil && realIdx >= 0 {
			sp.Tasks[realIdx].Done = !sp.Tasks[realIdx].Done
			m.clampSelection()
			m.save()
		}
		return m, nil

	case "a":
		if sp == nil {
			return m, nil
		}
		m.mode = "add"
		m.form = huh.NewForm(
			huh.NewGroup(
				huh.NewInput().
					Key("title").
					Title("New Task").
					Placeholder("What needs to be done?"),
			),
		).WithTheme(buildTheme()).WithWidth(44).WithShowHelp(false)
		return m, m.form.Init()

	case "backspace":
		realIdx := m.selectedRealIdx()
		if sp != nil && realIdx >= 0 {
			m.mode = "delete_confirm"
			m.form = huh.NewForm(
				huh.NewGroup(
					huh.NewConfirm().
						Key("confirm").
						Title(fmt.Sprintf("Delete '%s'?", sp.Tasks[realIdx].Title)).
						Affirmative("Yes, delete").
						Negative("Cancel"),
				),
			).WithTheme(buildDangerTheme()).WithWidth(44).WithShowHelp(false)
			return m, m.form.Init()
		}
		return m, nil

	case "ctrl+d":
		if sp != nil && len(sp.Tasks) > 0 {
			m.mode = "delete_all_confirm"
			m.form = huh.NewForm(
				huh.NewGroup(
					huh.NewConfirm().
						Key("confirm").
						Title(fmt.Sprintf("Delete all %d tasks?", len(sp.Tasks))).
						Affirmative("Yes, delete all").
						Negative("Cancel"),
				),
			).WithTheme(buildDangerTheme()).WithWidth(44).WithShowHelp(false)
			return m, m.form.Init()
		}
		return m, nil

	case "ctrl+shift+d":
		if sp != nil {
			m.mode = "delete_space_confirm"
			taskInfo := ""
			if len(sp.Tasks) > 0 {
				taskInfo = fmt.Sprintf(" and %d task(s)", len(sp.Tasks))
			}
			m.form = huh.NewForm(
				huh.NewGroup(
					huh.NewConfirm().
						Key("confirm").
						Title(fmt.Sprintf("Delete space '%s'%s?", sp.Name, taskInfo)).
						Affirmative("Yes, delete").
						Negative("Cancel"),
				),
			).WithTheme(buildDangerTheme()).WithWidth(44).WithShowHelp(false)
			return m, m.form.Init()
		}
		return m, nil

	case "ctrl+r":
		if sp != nil {
			m.mode = "rename_space"
			m.form = huh.NewForm(
				huh.NewGroup(
					huh.NewInput().
						Key("name").
						Title("Rename Space").
						Value(&sp.Name),
				),
			).WithTheme(buildTheme()).WithWidth(44).WithShowHelp(false)
			return m, m.form.Init()
		}
		return m, nil

	case "shift+up":
		if m.focusCol == 0 && sp != nil {
			todos := todoIndices(sp)
			if len(todos) > 1 && m.selectedIdx > 0 {
				a, b := todos[m.selectedIdx], todos[m.selectedIdx-1]
				sp.Tasks[a], sp.Tasks[b] = sp.Tasks[b], sp.Tasks[a]
				m.selectedIdx--
				m.save()
			}
		}
		return m, nil

	case "shift+down":
		if m.focusCol == 0 && sp != nil {
			todos := todoIndices(sp)
			if len(todos) > 1 && m.selectedIdx < len(todos)-1 {
				a, b := todos[m.selectedIdx], todos[m.selectedIdx+1]
				sp.Tasks[a], sp.Tasks[b] = sp.Tasks[b], sp.Tasks[a]
				m.selectedIdx++
				m.save()
			}
		}
		return m, nil
	}

	return m, nil
}

// View

func (m Model) View() string {
	if m.width == 0 || m.height == 0 {
		return "Loading..."
	}

	maxWidth := m.width - 4
	if maxWidth > 100 {
		maxWidth = 100
	}

	title := m.renderTitle()
	spaceIndicator := m.renderSpaceIndicator()
	progress := m.renderProgressBar(maxWidth)
	panelHeight := m.height - 14
	if panelHeight < 3 {
		panelHeight = 3
	}
	panels := m.renderColumns(maxWidth, panelHeight)
	footer := m.renderFooter()

	base := lipgloss.JoinVertical(
		lipgloss.Center,
		title,
		spaceIndicator,
		"",
		progress,
		panels,
		footer,
	)

	base = lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, base)

	if m.form != nil {
		box := styleDialogBox
		if m.mode == "delete_confirm" || m.mode == "delete_all_confirm" || m.mode == "delete_space_confirm" {
			box = box.BorderForeground(colorDanger)
		}
		dialog := box.Render(m.form.View())
		base = m.overlayCenter(base, dialog)
	}

	if m.mode == "help" {
		base = m.overlayCenter(base, m.renderHelpDialog())
	}

	return base
}

func (m Model) renderTitle() string {
	sp := m.activeSpace()
	if sp != nil {
		return styleTitle.Render("Kiroku — " + sp.Name)
	}
	return styleTitle.Render("Kiroku")
}

func (m Model) renderSpaceIndicator() string {
	if len(m.data.Spaces) == 0 {
		return styleMuted.Render("  Press Shift+Right to create a space")
	}

	var parts []string
	for i, s := range m.data.Spaces {
		if i == m.data.ActiveIdx {
			parts = append(parts, lipgloss.NewStyle().Foreground(colorPeach).Bold(true).Render(s.Name))
		} else {
			parts = append(parts, styleMuted.Render(s.Name))
		}
	}
	return "  " + strings.Join(parts, styleMuted.Render(" · "))
}

func (m Model) renderProgressBar(width int) string {
	sp := m.activeSpace()
	if sp == nil || len(sp.Tasks) == 0 {
		return ""
	}

	total := len(sp.Tasks)
	done := 0
	for _, t := range sp.Tasks {
		if t.Done {
			done++
		}
	}

	// border (2) + padding (2) = 4 chars consumed by the box
	innerWidth := width - 4
	if innerWidth < 5 {
		innerWidth = 5
	}

	label := fmt.Sprintf(" %d/%d", done, total)
	barWidth := innerWidth - len(label)
	if barWidth < 3 {
		barWidth = 3
	}

	filled := 0
	if total > 0 {
		filled = barWidth * done / total
	}
	empty := barWidth - filled

	filledStyle := lipgloss.NewStyle().Foreground(colorPeach)
	emptyStyle := lipgloss.NewStyle().Foreground(colorDim)
	labelStyle := lipgloss.NewStyle().Foreground(colorDim)

	bar := filledStyle.Render(strings.Repeat("█", filled)) +
		emptyStyle.Render(strings.Repeat("░", empty)) +
		labelStyle.Render(label)

	bordered := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorDim).
		Padding(0, 1).
		Width(width - 2). // match column total width (subtract border)
		Render(bar)

	return bordered
}

func (m Model) renderColumns(totalWidth, height int) string {
	sp := m.activeSpace()
	colWidth := (totalWidth - 1) / 2 // -1 for gap between columns

	leftContent := m.renderColumnItems(sp, 0)
	rightContent := m.renderColumnItems(sp, 1)

	focusedBorder := styleBorder.BorderForeground(colorPeach)
	dimBorder := styleBorder.BorderForeground(colorDim)

	todoLabel := styleMuted.Render("  To Do")
	doneLabel := styleMuted.Render("  Done")
	if m.focusCol == 0 {
		todoLabel = lipgloss.NewStyle().Foreground(colorPeach).Bold(true).Render("  To Do")
	} else {
		doneLabel = lipgloss.NewStyle().Foreground(colorPeach).Bold(true).Render("  Done")
	}

	var leftPanel, rightPanel string
	if m.focusCol == 0 {
		leftPanel = focusedBorder.Width(colWidth - 2).Height(height).Render(leftContent)
		rightPanel = dimBorder.Width(colWidth - 2).Height(height).Render(rightContent)
	} else {
		leftPanel = dimBorder.Width(colWidth - 2).Height(height).Render(leftContent)
		rightPanel = focusedBorder.Width(colWidth - 2).Height(height).Render(rightContent)
	}

	leftCol := lipgloss.JoinVertical(lipgloss.Left, todoLabel, leftPanel)
	rightCol := lipgloss.JoinVertical(lipgloss.Left, doneLabel, rightPanel)

	return lipgloss.JoinHorizontal(lipgloss.Top, leftCol, " ", rightCol)
}

func (m Model) renderColumnItems(sp *Space, col int) string {
	if sp == nil {
		return styleMuted.Render("No spaces yet.\nPress Shift+Right\nto create one.")
	}

	var indices []int
	if col == 0 {
		indices = todoIndices(sp)
	} else {
		indices = doneIndices(sp)
	}

	if len(indices) == 0 {
		if col == 0 {
			return styleMuted.Render("No tasks yet.\nPress 'a' to add.")
		}
		return styleMuted.Render("Nothing completed\nyet.")
	}

	isFocused := m.focusCol == col
	var items []string
	for listIdx, realIdx := range indices {
		task := sp.Tasks[realIdx]
		checkbox := "[ ]"
		if task.Done {
			checkbox = "[x]"
		}
		label := fmt.Sprintf("%s %s", checkbox, task.Title)

		var item string
		if isFocused && listIdx == m.selectedIdx {
			if task.Done {
				item = styleDoneSelected.Render("▸ " + label)
			} else {
				item = styleSelected.Render("▸ " + label)
			}
		} else {
			if task.Done {
				item = styleDone.Render("  " + label)
			} else {
				item = styleUnselected.Render("  " + label)
			}
		}
		items = append(items, item)
	}

	return strings.Join(items, "\n")
}

func (m Model) renderFooter() string {
	return styleHelp.Render("Press ? for help")
}

func (m Model) renderHelpDialog() string {
	keyStyle := lipgloss.NewStyle().Foreground(colorPeach).Bold(true).Width(20)
	descStyle := lipgloss.NewStyle().Foreground(colorText)
	sectionStyle := lipgloss.NewStyle().Foreground(colorLavend).Bold(true).MarginTop(1)

	lines := []string{
		sectionStyle.Render("Tasks"),
		keyStyle.Render("a") + descStyle.Render("Add task"),
		keyStyle.Render("Space / Enter") + descStyle.Render("Toggle done/undone"),
		keyStyle.Render("Backspace") + descStyle.Render("Delete task"),
		keyStyle.Render("Ctrl+D") + descStyle.Render("Delete all tasks"),
		keyStyle.Render("Shift+Up/Down") + descStyle.Render("Reorder task"),
		keyStyle.Render("Up/Down  j/k") + descStyle.Render("Navigate"),
		keyStyle.Render("Tab") + descStyle.Render("Switch column"),
		"",
		sectionStyle.Render("Spaces"),
		keyStyle.Render("Shift+Left/Right") + descStyle.Render("Switch space"),
		keyStyle.Render("Shift+Right") + descStyle.Render("New space (past last)"),
		keyStyle.Render("Ctrl+R") + descStyle.Render("Rename space"),
		keyStyle.Render("Ctrl+Shift+D") + descStyle.Render("Delete space"),
		"",
		sectionStyle.Render("General"),
		keyStyle.Render("?") + descStyle.Render("Toggle this help"),
		keyStyle.Render("q") + descStyle.Render("Quit"),
	}

	content := strings.Join(lines, "\n")

	return styleDialogBox.Width(46).Render(content)
}

// Overlay

func (m Model) overlayCenter(bg, fg string) string {
	bgLines := strings.Split(bg, "\n")
	fgLines := strings.Split(fg, "\n")

	bgH := len(bgLines)
	fgH := len(fgLines)

	fgW := 0
	for _, line := range fgLines {
		w := lipgloss.Width(line)
		if w > fgW {
			fgW = w
		}
	}

	startY := (bgH - fgH) / 2
	startX := (m.width - fgW) / 2
	if startX < 0 {
		startX = 0
	}

	for i, fgLine := range fgLines {
		y := startY + i
		if y < 0 || y >= bgH {
			continue
		}

		bgLine := bgLines[y]
		bgLineW := lipgloss.Width(bgLine)
		if bgLineW < m.width {
			bgLine += strings.Repeat(" ", m.width-bgLineW)
		}

		left := truncateToWidth(bgLine, startX)
		rightStart := startX + fgW
		right := ""
		if rightStart < m.width {
			right = skipToWidth(bgLine, rightStart)
		}

		bgLines[y] = left + fgLine + right
	}

	return strings.Join(bgLines, "\n")
}

func truncateToWidth(s string, w int) string {
	result := ""
	col := 0
	inEsc := false
	for _, r := range s {
		if r == '\x1b' {
			inEsc = true
			result += string(r)
			continue
		}
		if inEsc {
			result += string(r)
			if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') {
				inEsc = false
			}
			continue
		}
		if col >= w {
			break
		}
		result += string(r)
		col++
	}
	for col < w {
		result += " "
		col++
	}
	return result
}

func skipToWidth(s string, w int) string {
	result := ""
	col := 0
	inEsc := false
	skipping := true
	for _, r := range s {
		if r == '\x1b' {
			inEsc = true
			if !skipping {
				result += string(r)
			}
			continue
		}
		if inEsc {
			if !skipping {
				result += string(r)
			}
			if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') {
				inEsc = false
			}
			continue
		}
		if skipping {
			col++
			if col >= w {
				skipping = false
			}
			continue
		}
		result += string(r)
	}
	return result
}

func main() {
	path := dataFilePath()
	data, err := loadData(path)
	if err != nil {
		// File doesn't exist or is invalid — start fresh.
		data = AppData{}
	}

	m := Model{
		data:     data,
		dataPath: path,
		mode:     "normal",
	}

	p := tea.NewProgram(m, tea.WithAltScreen())

	if _, err := p.Run(); err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}
}
