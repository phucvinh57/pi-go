package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/kballard/go-shellquote"

	"github.com/phucvinh57/pi-go/internal/prompt"
)

const maxInputLines = 8

// submitMsg submits text as if the user had typed it and pressed Enter.
type submitMsg struct{ text string }

type doneMsg struct {
	text string
	err  error
}

type choicesMsg struct {
	items []string
	err   error // providers that could not be listed; items may still be set

	asDefault bool // the choice is also saved as the default model
}

type app struct {
	ctx  context.Context
	opts Options

	input textarea.Model
	spin  spinner.Model

	tr       transcript
	vp       viewport.Model
	expanded bool // show tool output and reasoning in full
	unseen   bool // output arrived while the user was scrolled up
	sel      selection
	clip     Clipboard
	copied   bool // the last selection was copied; shown until the next key

	suggestions []suggestion
	selected    int
	files       []string // files under root that can be tagged; loaded while an "@" is being typed

	picker      *picker   // non-nil while the user is choosing a model
	pickDefault bool      // the open picker saves the choice as the default model
	ask         *question // non-nil while a running command asks the user something
	current     string
	stats       *Stats // usage of the session so far; nil before the first model call

	hist history

	busy     bool
	cancel   context.CancelFunc
	aborted  bool // the user cancelled the running prompt
	started  time.Time
	status   string // what the agent is doing, e.g. "running bash"
	quitting bool

	// send reaches the program from the prompt's goroutine. Run sets it; until
	// then (in tests) it drops what it is given.
	send func(tea.Msg)

	cwd           string // for display
	root          string // where "@" tags are resolved
	width, height int
}

func newApp(ctx context.Context, opts Options) *app {
	in := textarea.New()
	in.Prompt = promptStyle.Render("> ")
	in.Placeholder = "Message, / for commands, @ to tag a file"
	in.ShowLineNumbers = false
	in.DynamicHeight = true
	in.MinHeight = 1
	in.MaxHeight = maxInputLines
	// Enter submits, so a newline needs another key. Shift+Enter only reaches
	// us on terminals with keyboard enhancements; Ctrl+J works everywhere.
	in.KeyMap.InsertNewline = key.NewBinding(key.WithKeys("shift+enter", "ctrl+j"))
	in.SetHeight(1)
	in.SetVirtualCursor(false) // View hands the real cursor to the terminal
	// The box around the input is the focus indicator, so drop the highlighted
	// cursor row.
	st := in.Styles()
	st.Focused.CursorLine = lipgloss.NewStyle()
	st.Blurred.CursorLine = lipgloss.NewStyle()
	in.SetStyles(st)

	vp := viewport.New()
	vp.KeyMap = viewport.KeyMap{} // keys are routed by scrollKey; the input owns the rest

	if opts.OnPrompt == nil {
		opts.OnPrompt = func(context.Context, string, func(Event)) error {
			return errors.New("no agent is configured")
		}
	}

	root, _ := os.Getwd()
	return &app{
		ctx:   ctx,
		opts:  opts,
		input: in,
		vp:    vp,
		spin:  spinner.New(spinner.WithSpinner(spinner.Dot)),
		cwd:   shortCwd(),
		root:  root,
		send:  func(tea.Msg) {},
		clip:  opts.Clipboard,
	}
}

func (m *app) Init() tea.Cmd {
	if m.opts.Models != nil {
		m.current = m.opts.Models.Current()
	}
	m.tr.add(entry{kind: kindWelcome, text: m.opts.Version})
	cmds := []tea.Cmd{m.input.Focus()}
	if m.opts.InitialPrompt != "" {
		text := m.opts.InitialPrompt
		cmds = append(cmds, func() tea.Msg { return submitMsg{text} })
	}
	return tea.Batch(cmds...)
}

func (m *app) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	_, cmd := m.update(msg)
	m.layout() // the input, the popup or the window may have changed size
	return m, cmd
}

func (m *app) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.sel = selection{} // the lines were rewrapped
		return m, nil

	case tea.MouseClickMsg, tea.MouseMotionMsg, tea.MouseReleaseMsg:
		cmd := m.onMouse(msg.(tea.MouseMsg))
		m.copied = cmd != nil
		return m, cmd

	case tea.MouseWheelMsg:
		m.vp, _ = m.vp.Update(msg)
		m.unseen = m.unseen && !m.vp.AtBottom()
		return m, nil

	case eventMsg:
		m.onEvent(msg.ev)
		return m, nil

	case askMsg:
		m.ask = newQuestion(msg.req)
		return m, nil

	case askCancelMsg:
		if m.ask != nil && m.ask.req == msg.req {
			m.ask = nil
		}
		return m, nil

	case tea.PasteMsg:
		if m.ask != nil {
			return m, m.onAskKey(msg)
		}

	case tea.KeyPressMsg:
		return m.onKey(msg)

	case submitMsg:
		return m, m.submit(msg.text)

	case doneMsg:
		m.finish()
		if m.aborted {
			m.aborted = false
			m.addEntry(entry{kind: kindNotice, text: "aborted"})
		} else {
			m.report(msg)
		}
		return m, m.input.Focus()

	case choicesMsg:
		m.finish()
		if msg.err != nil {
			m.addEntry(entry{kind: kindError, text: strings.TrimRight(msg.err.Error(), "\n")})
		}
		if len(msg.items) == 0 {
			if msg.err == nil {
				m.addEntry(entry{kind: kindNotice, text: "no models available"})
			}
			return m, m.input.Focus()
		}
		title := "Select a model"
		if msg.asDefault {
			title = "Select the default model"
		}
		m.picker = newPicker(title, msg.items, m.current)
		m.pickDefault = msg.asDefault
		return m, nil

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

func (m *app) onKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	m.sel, m.copied = selection{}, false
	if m.scrollKey(msg.String()) {
		return m, nil
	}
	if m.ask != nil {
		return m, m.onAskKey(msg)
	}
	if m.picker != nil {
		return m.onPickerKey(msg)
	}
	if k := msg.String(); k != "up" && k != "down" {
		m.hist.reset() // any other key ends history navigation
	}

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
		// Enter finishes a half-typed file tag; it submits once the tag is whole.
		if s, ok := m.selectedFile(); ok && !m.tagComplete(s) {
			m.accept(s)
			return m, nil
		}
		return m, m.submit(m.input.Value())

	case "tab":
		if len(m.suggestions) > 0 {
			m.accept(m.suggestions[m.selected])
			return m, nil
		}

	case "up", "down":
		up := msg.String() == "up"
		// An open suggestion menu owns the arrows, unless we are already
		// walking through history (recalled text never opens the menu).
		if m.hist.navigating() || len(m.suggestions) == 0 {
			if m.navigateHistory(up) {
				return m, nil
			}
		}
		if n := len(m.suggestions); n > 0 {
			step := 1
			if up {
				step = n - 1
			}
			m.selected = (m.selected + step) % n
			return m, nil
		}
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	m.input.SetHeight(min(max(m.input.LineCount(), 1), maxInputLines))
	if !m.hist.navigating() {
		m.refreshSuggestions()
	}
	return m, cmd
}

// atTop and atBottom report whether the cursor is on the first or last visual
// row of the input, counting soft-wrapped rows. Only there do the arrow keys
// move through history instead of through the text.
func (m *app) atTop() bool {
	return m.input.Line() == 0 && m.input.LineInfo().RowOffset == 0
}

func (m *app) atBottom() bool {
	li := m.input.LineInfo()
	return m.input.Line() == m.input.LineCount()-1 && li.RowOffset == li.Height-1
}

// navigateHistory recalls an older (up) or newer entry into the editor and
// reports whether it did.
func (m *app) navigateHistory(up bool) bool {
	var (
		text string
		ok   bool
	)
	if up {
		if !m.atTop() {
			return false
		}
		text, ok = m.hist.prev(m.input.Value())
	} else {
		if !m.hist.navigating() || !m.atBottom() {
			return false
		}
		text, ok = m.hist.next(m.input.Value())
	}
	if !ok {
		return false
	}

	m.input.SetValue(text)
	if up {
		m.input.MoveToBegin()
		m.input.CursorEnd()
	} else {
		m.input.MoveToEnd()
	}
	m.input.SetHeight(min(max(m.input.LineCount(), 1), maxInputLines))
	if m.hist.navigating() {
		m.suggestions, m.selected = nil, 0
	} else {
		m.refreshSuggestions() // the original draft is back
	}
	return true
}

// refreshSuggestions rebuilds the completion menu: slash commands while the
// input starts with "/", files while its last word starts with "@".
func (m *app) refreshSuggestions() {
	m.suggestions = nil
	m.selected = 0
	value := m.input.Value()
	if m.opts.NewCommand != nil {
		m.suggestions = suggest(m.opts.NewCommand, value)
	}
	if len(m.suggestions) > 0 {
		return
	}

	query, ok := trailingTag(value)
	if !ok {
		m.files = nil // reread the tree the next time a tag starts
		return
	}
	if m.files == nil {
		m.files = listFiles(m.root)
	}
	for _, f := range matchFiles(m.files, query) {
		m.suggestions = append(m.suggestions, suggestion{Name: f, File: true})
	}
}

// selectedFile returns the highlighted suggestion if it is a file.
func (m *app) selectedFile() (suggestion, bool) {
	if len(m.suggestions) == 0 || !m.suggestions[m.selected].File {
		return suggestion{}, false
	}
	return m.suggestions[m.selected], true
}

// tagComplete reports whether the tag being typed already names s.
func (m *app) tagComplete(s suggestion) bool {
	query, _ := trailingTag(m.input.Value())
	return query == s.Name
}

// accept puts a suggestion into the input: a command name after the "/", or a
// file path in place of the "@" tag being typed.
func (m *app) accept(s suggestion) {
	if s.File {
		query, _ := trailingTag(m.input.Value())
		value := m.input.Value()
		m.input.SetValue(value[:len(value)-len(query)] + s.Name + " ")
	} else {
		m.input.SetValue("/" + s.Name + " ")
	}
	m.refreshSuggestions()
}

func (m *app) onPickerKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		m.picker.move(-1)
	case "down", "j":
		m.picker.move(1)
	case "esc", "ctrl+c":
		m.picker = nil
		return m, m.input.Focus()
	case "enter":
		ref := m.picker.choice()
		m.picker = nil
		return m, tea.Batch(m.input.Focus(), m.selectModel(ref, m.pickDefault))
	}
	return m, nil
}

func (m *app) abort() {
	if m.cancel != nil {
		m.aborted = true
		m.cancel()
	}
}

func (m *app) finish() {
	m.busy = false
	m.cancel = nil
	m.status = ""
}

func (m *app) submit(text string) tea.Cmd {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	m.hist.add(text)
	m.input.Reset()
	m.input.SetHeight(1)
	m.refreshSuggestions()

	m.vp.GotoBottom() // sending a message follows the conversation again
	m.addEntry(entry{kind: kindUser, text: text})

	if !strings.HasPrefix(text, "/") {
		// The transcript shows what the user typed; the model also gets the
		// tagged files.
		sent, tagged := expandTags(m.root, text)
		if len(tagged) > 0 {
			m.addEntry(entry{kind: kindNotice, text: "attached " + strings.Join(tagged, ", ")})
		}
		return m.background(func(ctx context.Context) (string, error) {
			emit := func(ev Event) { m.send(eventMsg{ev}) }
			return "", m.opts.OnPrompt(ctx, sent, emit)
		})
	}

	argv, err := shellquote.Split(text[1:])
	if err != nil || len(argv) == 0 {
		msg := "type /help to list commands"
		if err != nil {
			msg = err.Error()
		}
		m.addEntry(entry{kind: kindError, text: msg})
		return nil
	}

	if b, ok := builtins[argv[0]]; ok {
		return b.run(m, argv[1:])
	}
	return m.background(func(ctx context.Context) (string, error) {
		// Commands ask their questions on this screen, not on the terminal.
		ctx = prompt.With(ctx, asker{func(msg tea.Msg) { m.send(msg) }})
		return runCobra(ctx, m.opts.NewCommand, argv)
	})
}

// background runs work off the UI goroutine and reports the result as a
// doneMsg.
func (m *app) background(work func(context.Context) (string, error)) tea.Cmd {
	return m.backgroundMsg(func(ctx context.Context) tea.Msg {
		text, err := work(ctx)
		return doneMsg{text: text, err: err}
	})
}

// backgroundMsg is background for work that reports its result as a message
// other than doneMsg. The message handler must clear m.busy.
func (m *app) backgroundMsg(work func(context.Context) tea.Msg) tea.Cmd {
	ctx, cancel := context.WithCancel(m.ctx)
	m.cancel = cancel
	m.busy = true
	m.aborted = false
	m.started = time.Now()
	m.input.Blur()

	run := func() tea.Msg {
		defer cancel()
		return work(ctx)
	}
	return tea.Batch(run, m.spin.Tick)
}

func (m *app) activity() string {
	if m.status != "" {
		return m.status
	}
	return "working"
}

func elapsed(d time.Duration) string {
	d = d.Round(time.Second)
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
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

// addEntry appends to the transcript and follows it if the user is at the bottom.
func (m *app) addEntry(e entry) {
	m.tr.add(e)
	m.refresh(true)
}

// report shows the outcome of a slash command in the transcript. A prompt
// reports nothing here: its reply arrived as events.
func (m *app) report(d doneMsg) {
	text := strings.TrimRight(d.text, "\n")
	switch {
	case d.err == nil && text == "":
	case d.err == nil:
		m.addEntry(entry{kind: kindInfo, text: text})
	case text == "":
		m.addEntry(entry{kind: kindError, text: "error: " + d.err.Error()})
	default:
		// Cobra already printed the error text, or the command silenced it.
		m.addEntry(entry{kind: kindError, text: text})
	}
}

func (m *app) onEvent(ev Event) {
	switch ev.Kind {
	case EventText:
		m.status = "responding"
		m.appendText(kindAssistant, ev.Text)
	case EventThinking:
		m.status = "thinking"
		m.appendText(kindThinking, ev.Text)
	case EventToolStart:
		m.status = "running " + ev.Tool
		m.addEntry(entry{kind: kindTool, tool: ev.Tool, args: ev.Args})
	case EventToolEnd:
		m.status = ""
		if e := m.tr.openTool(ev.Tool); e != nil {
			e.output, e.isError, e.done = ev.Text, ev.IsError, true
			e.touch()
		}
		m.refresh(true)
	case EventStats:
		m.stats = ev.Stats
	case EventWarning:
		m.addEntry(entry{kind: kindError, text: "warning: " + ev.Text})
	}
}

func (m *app) appendText(kind entryKind, delta string) {
	if e := m.tr.last(); e != nil && e.kind == kind {
		e.text += delta
		e.touch()
		m.refresh(true)
		return
	}
	m.addEntry(entry{kind: kind, text: delta})
}

// refresh redraws the transcript into the viewport. If it was scrolled to the
// bottom it stays there; otherwise the position is kept, and newOutput marks
// that there is something below.
func (m *app) refresh(newOutput bool) {
	follow := m.vp.AtBottom()
	m.vp.SetContent(m.tr.render(m.vp.Width(), m.expanded))
	switch {
	case follow:
		m.vp.GotoBottom()
		m.unseen = false
	case newOutput:
		m.unseen = true
	}
}

// scrollKey handles the keys that move through or reshape the transcript, and
// reports whether it took the key.
func (m *app) scrollKey(k string) bool {
	switch k {
	case "pgup":
		m.vp.PageUp()
	case "pgdown":
		m.vp.PageDown()
	case "shift+up":
		m.vp.ScrollUp(3)
	case "shift+down":
		m.vp.ScrollDown(3)
	case "ctrl+home":
		m.vp.GotoTop()
	case "ctrl+end":
		m.vp.GotoBottom()
	case "ctrl+o":
		m.expanded = !m.expanded
		m.refresh(false)
	default:
		return false
	}
	m.unseen = m.unseen && !m.vp.AtBottom()
	return true
}

func (m *app) layout() {
	if m.width == 0 || m.height == 0 {
		return
	}
	m.input.SetWidth(max(m.width-4, 8)) // the box takes a border and a space on each side

	status, box, popup, footer := m.bottom()
	below := lipgloss.Height(status) + lipgloss.Height(box) + lipgloss.Height(footer)
	if popup != "" {
		below += lipgloss.Height(popup)
	}

	follow := m.vp.AtBottom()
	resized := m.vp.Width() != m.width
	m.vp.SetWidth(m.width)
	m.vp.SetHeight(max(m.height-below, 1))
	if resized {
		m.vp.SetContent(m.tr.render(m.width, m.expanded))
	}
	if follow {
		m.vp.GotoBottom()
	}
}

// bottom draws everything under the transcript: a status row, the input box
// (or the model picker in its place), the suggestion menu, and the footer.
func (m *app) bottom() (status, box, popup, footer string) {
	width := m.width
	if width == 0 {
		width = 80 // before the first size report
	}
	switch {
	case m.ask != nil:
		// The question is on screen; the status row would only distract.
	case m.busy:
		status = m.spin.View() + dimStyle.Render(fmt.Sprintf(" %s... %s · esc to cancel", m.activity(), elapsed(time.Since(m.started))))
	case m.copied:
		status = dimStyle.Render("copied to clipboard")
	case m.unseen:
		status = dimStyle.Render("↓ new output · ctrl+end to follow")
	}

	border := promptStyle
	if (m.busy && m.ask == nil) || m.picker != nil {
		border = dimStyle
	}
	body := m.input.View()
	switch {
	case m.ask != nil:
		body = m.ask.view()
	case m.picker != nil:
		body = m.picker.view()
	}
	frame := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(border.GetForeground()).
		Padding(0, 1).
		Width(width)
	box = frame.Render(body)

	if m.picker == nil && m.ask == nil && !m.busy {
		popup = m.suggestionMenu(width)
	}

	parts := []string{m.cwd}
	if m.current != "" {
		parts = append(parts, m.current)
	}
	parts = append(parts, "/ for commands", "@ to tag a file")
	footer = dimStyle.Render(ansi.Truncate(" "+strings.Join(parts, " · "), width, "…"))
	if line := m.statsLine(width); line != "" {
		footer = line + "\n" + footer
	}
	return status, box, popup, footer
}

// statsLine is the footer row with the session's usage: tokens, cache, cost and
// how full the context is. It is empty until there is something to show.
func (m *app) statsLine(width int) string {
	if m.stats == nil {
		return ""
	}
	parts := m.stats.usageParts()
	ctx := m.stats.contextPart()
	if len(parts) == 0 && ctx == "" {
		return ""
	}

	// Truncate the plain text first, then colour: the context figure is the one
	// that must survive a narrow terminal, so it goes first and the rest is cut.
	lead := ""
	if len(parts) > 0 {
		lead = strings.Join(parts, " · ")
	}
	room := width - 1 // the leading space
	if ctx != "" {
		room -= ansi.StringWidth(ctx)
		if lead != "" {
			room -= ansi.StringWidth(" · ")
		}
	}
	var b strings.Builder
	b.WriteString(dimStyle.Render(" "))
	if lead != "" && room > 0 {
		b.WriteString(dimStyle.Render(ansi.Truncate(lead, room, "…")))
		if ctx != "" {
			b.WriteString(dimStyle.Render(" · "))
		}
	}
	if ctx != "" {
		style := dimStyle
		switch {
		case m.stats.ContextPercent > ctxErrorPercent:
			style = errorStyle
		case m.stats.ContextPercent > ctxWarnPercent:
			style = warnStyle
		}
		b.WriteString(style.Render(ctx))
	}
	return b.String()
}

const maxSuggestionRows = 8

// suggestionMenu lists the slash commands matching the input, scrolled to keep
// the selected one in view.
func (m *app) suggestionMenu(width int) string {
	n := len(m.suggestions)
	if n == 0 {
		return ""
	}
	first := min(max(m.selected-maxSuggestionRows+1, 0), max(n-maxSuggestionRows, 0))
	last := min(first+maxSuggestionRows, n)

	var rows []string
	for i := first; i < last; i++ {
		s := m.suggestions[i]
		sigil := "/"
		if s.File {
			sigil = "@"
		}
		line := "  " + sigil + s.Name + "  " + dimStyle.Render(s.Desc)
		if i == m.selected {
			line = selStyle.Render("› "+sigil+s.Name) + "  " + dimStyle.Render(s.Desc)
		}
		rows = append(rows, ansi.Truncate(" "+line, width, "…"))
	}
	return strings.Join(rows, "\n")
}

// transcriptView is the visible part of the conversation, with the selection
// drawn on it.
func (m *app) transcriptView() string {
	if m.sel.empty() {
		return m.vp.View()
	}
	rows := strings.Split(m.vp.View(), "\n")
	return strings.Join(m.sel.highlight(rows, m.vp.YOffset()), "\n")
}

func (m *app) View() tea.View {
	if m.quitting {
		return tea.NewView("") // leaves the alternate screen
	}

	status, box, popup, footer := m.bottom()
	parts := []string{m.transcriptView(), status, box}
	if popup != "" {
		parts = append(parts, popup)
	}
	parts = append(parts, footer)

	v := tea.NewView(strings.Join(parts, "\n"))
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	v.WindowTitle = "pi-go"
	if m.current != "" {
		v.WindowTitle += " · " + m.current
	}
	if c := m.input.Cursor(); c != nil && !m.busy && m.picker == nil && m.ask == nil {
		c.X += 2                     // the box's border and padding
		c.Y += m.vp.Height() + 1 + 1 // the transcript, the status row, the box's top border
		v.Cursor = c
	}
	return v
}
