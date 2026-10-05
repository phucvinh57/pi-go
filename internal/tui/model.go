package tui

import (
	"context"
	"errors"
	"os"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/kballard/go-shellquote"
)

const maxInputLines = 8

var (
	promptStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Bold(true)
	errorStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	dimStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	selStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
)

// submitMsg submits text as if the user had typed it and pressed Enter.
type submitMsg struct{ text string }

// doneMsg is the result of a slash command or prompt that ran in the background.
type doneMsg struct {
	text string
	err  error
}

type model struct {
	ctx  context.Context
	opts Options

	input textarea.Model
	spin  spinner.Model

	suggestions []suggestion
	selected    int

	busy     bool
	cancel   context.CancelFunc
	quitting bool

	cwd   string
	width int
}

func newModel(ctx context.Context, opts Options) *model {
	in := textarea.New()
	in.Prompt = promptStyle.Render("> ")
	in.Placeholder = "Message, or / for commands"
	in.ShowLineNumbers = false
	in.DynamicHeight = true
	in.MinHeight = 1
	in.MaxHeight = maxInputLines
	// Enter submits, so a newline needs another key. Shift+Enter only reaches
	// us on terminals with keyboard enhancements; Ctrl+J works everywhere.
	in.KeyMap.InsertNewline = key.NewBinding(key.WithKeys("shift+enter", "ctrl+j"))
	in.SetHeight(1)
	in.SetVirtualCursor(false) // View hands the real cursor to the terminal

	if opts.OnPrompt == nil {
		opts.OnPrompt = func(context.Context, string) (string, error) {
			return "", errors.New("no agent is configured")
		}
	}

	return &model{
		ctx:   ctx,
		opts:  opts,
		input: in,
		spin:  spinner.New(spinner.WithSpinner(spinner.Dot)),
		cwd:   shortCwd(),
	}
}

func (m *model) Init() tea.Cmd {
	cmds := []tea.Cmd{m.input.Focus()}
	if m.opts.InitialPrompt != "" {
		text := m.opts.InitialPrompt
		cmds = append(cmds, func() tea.Msg { return submitMsg{text} })
	}
	return tea.Batch(cmds...)
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.input.SetWidth(msg.Width)
		return m, nil

	case tea.KeyPressMsg:
		return m.onKey(msg)

	case submitMsg:
		return m, m.submit(msg.text)

	case doneMsg:
		m.busy = false
		m.cancel = nil
		return m, tea.Batch(m.input.Focus(), m.print(render(msg)))

	case spinner.TickMsg:
		if !m.busy {
			return m, nil
		}
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd
	}

	// Cursor blink and anything else the editor cares about.
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m *model) onKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		switch {
		case m.busy:
			m.abort()
		case m.input.Value() != "":
			m.input.Reset()
			m.refreshSuggestions()
		default:
			return m, runQuit(m, nil)
		}
		return m, nil

	case "ctrl+d":
		if !m.busy && m.input.Value() == "" {
			return m, runQuit(m, nil)
		}

	case "esc":
		if m.busy {
			m.abort()
		}
		return m, nil
	}

	if m.busy {
		return m, nil
	}

	switch msg.String() {
	case "enter":
		return m, m.submit(m.input.Value())

	case "tab":
		if len(m.suggestions) > 0 {
			m.input.SetValue("/" + m.suggestions[m.selected].Name + " ")
			m.refreshSuggestions()
			return m, nil
		}

	case "up", "down":
		if n := len(m.suggestions); n > 0 {
			step := 1
			if msg.String() == "up" {
				step = n - 1
			}
			m.selected = (m.selected + step) % n
			return m, nil
		}
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	m.input.SetHeight(min(max(m.input.LineCount(), 1), maxInputLines))
	m.refreshSuggestions()
	return m, cmd
}

func (m *model) refreshSuggestions() {
	m.suggestions = nil
	m.selected = 0
	if m.opts.NewCommand != nil {
		m.suggestions = suggest(m.opts.NewCommand, m.input.Value())
	}
}

func (m *model) abort() {
	if m.cancel != nil {
		m.cancel()
	}
}

// submit sends text to the right handler: a slash command or a prompt.
func (m *model) submit(text string) tea.Cmd {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	m.input.Reset()
	m.input.SetHeight(1)
	m.refreshSuggestions()

	echo := m.print(promptStyle.Render("> ") + text)

	if !strings.HasPrefix(text, "/") {
		return m.background(echo, func(ctx context.Context) (string, error) {
			return m.opts.OnPrompt(ctx, text)
		})
	}

	argv, err := shellquote.Split(text[1:])
	if err != nil || len(argv) == 0 {
		msg := "type /help to list commands"
		if err != nil {
			msg = err.Error()
		}
		return tea.Sequence(echo, m.print(errorStyle.Render(msg)))
	}

	if b, ok := builtins[argv[0]]; ok {
		return tea.Sequence(echo, b.run(m, argv[1:]))
	}
	return m.background(echo, func(ctx context.Context) (string, error) {
		return runCobra(ctx, m.opts.NewCommand, argv)
	})
}

// background runs work off the UI goroutine after echo has been printed, and
// reports the result as a doneMsg.
func (m *model) background(echo tea.Cmd, work func(context.Context) (string, error)) tea.Cmd {
	ctx, cancel := context.WithCancel(m.ctx)
	m.cancel = cancel
	m.busy = true
	m.input.Blur()

	run := func() tea.Msg {
		defer cancel()
		text, err := work(ctx)
		return doneMsg{text: text, err: err}
	}
	return tea.Batch(tea.Sequence(echo, run), m.spin.Tick)
}

// print writes text above the live area, into the terminal's scrollback.
func (m *model) print(text string) tea.Cmd {
	return tea.Println(text)
}

func render(d doneMsg) string {
	text := strings.TrimRight(d.text, "\n")
	switch {
	case d.err == nil:
		return text
	case text == "":
		return errorStyle.Render("error: " + d.err.Error())
	default:
		// Cobra already printed the error text, or the command silenced it.
		return errorStyle.Render(text)
	}
}

func (m *model) View() tea.View {
	if m.quitting {
		return tea.NewView("")
	}

	var b strings.Builder
	if m.busy {
		b.WriteString(m.spin.View() + dimStyle.Render(" working... (esc to cancel)"))
	} else {
		b.WriteString(m.input.View())
		for i, s := range m.suggestions {
			line := "  /" + s.Name
			if s.Desc != "" {
				line += "  " + dimStyle.Render(s.Desc)
			}
			if i == m.selected {
				line = selStyle.Render("› /"+s.Name) + "  " + dimStyle.Render(s.Desc)
			}
			b.WriteString("\n" + line)
		}
	}
	footer := dimStyle
	if m.width > 0 {
		footer = footer.MaxWidth(m.width) // a wrapped line garbles the inline redraw
	}
	b.WriteString("\n" + footer.Render(m.cwd+" · / for commands · ctrl+j for newline"))

	v := tea.NewView(b.String())
	if c := m.input.Cursor(); c != nil && !m.busy {
		v.Cursor = c
	}
	return v
}

func shortCwd() string {
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(cwd, home) {
		return "~" + strings.TrimPrefix(cwd, home)
	}
	return cwd
}
