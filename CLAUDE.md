# CLAUDE.md

pi-go is a Go clone of the [PI](https://github.com/earendil-works/pi) coding agent, built as a learning project for harness engineering.
## Commands

```sh
go build ./...
go vet ./...
go test ./...
go test ./internal/auth -run TestName   # single test
go run ./cmd/pi-go [flags] [messages...]
go build -ldflags "-X pi-go/internal/cli.Version=1.2.3" -o pi-go ./cmd/pi-go   # stamp the version
```

No linter or Makefile is configured. Requires Go 1.27+ (see `go.mod`).

## Architecture

- `cmd/pi-go/main.go` only wires `cli.New(commands.All)` and exits `1` on any error. Typed exit codes (`auth check`: 0/1/2) are planned, not done.
- `internal/cli`: the root `pi` command. It picks interactive vs print mode (`chooseMode`: a TTY on both stdin and stdout and no `-p` means interactive). `respond.go` builds the agent lazily on the first prompt (default model `ollama/llama3.2:latest`, flags `--provider`/`--model`) and keeps it for the session.
- `internal/commands`: thin cobra adapters (parse flags, call a feature package, print). Business rules do not live here. `commands.All()` is the registry.
- `internal/tui`: the Bubble Tea v2 interactive session (`charm.land/bubbletea/v2`, not the old `github.com/charmbracelet/bubbletea`). It is full screen (alt screen, mouse wheel on): a scrollable `viewport` of the conversation, a rounded input box pinned below it, then a status row and a footer. The conversation is data (`transcript.go`: a list of `entry` values drawn at the current width, so a resize rewraps; `markdown.go` renders assistant text), not scrollback. While a prompt runs, the agent's events reach `Update` as `eventMsg` (via `p.Send` from the prompt's goroutine, which keeps order) and are folded into the transcript. `layout()` runs after every `Update` and sizes the viewport from what `bottom()` draws below it. On quit, `Run` prints the plain transcript to `Options.Out` so it stays in the user's scrollback. Slash commands run TUI built-ins (`slash.go`) first, otherwise the **same cobra tree** as the shell via `bridge.go`, with output captured into the transcript. `/model` is a built-in (`/model [--default] [provider/id]`; `--default` also saves the choice): the TUI sees the active model only through the `tui.Models` interface, which `cli`'s `session` (in `respond.go`) implements.
- `internal/tools`: the built-in tools `read`, `bash`, `edit`, `write`. Each is a `Tool` (a `Spec` plus `Execute(ctx, rawJSON, onUpdate)`); a returned error is the message the model sees. Knows nothing about `ai`; the agent loop adapts `Spec` to `ai.Tool`. `tools.Core(cwd)` returns all four.
- `internal/agent`: the loop. `Agent.Prompt` (or `PromptWith`, which also emits text/tool events) appends a user message, calls the model, runs the requested tools, and repeats until a reply has no tool calls (max 25 model calls). A failed prompt rolls the conversation back. Also builds the system prompt from the active tools. It is the one feature package that imports `ai` and `tools`.
- `internal/prompt`: how a command asks the user something (`Prompter`: `Select` a list, `Input` a line, optionally secret). `Terminal` draws inline and reads raw keys, so ctrl+c/esc always cancel (`ErrCancelled`); `Lines` reads piped stdin. The TUI puts its own on-screen `Prompter` in the command's context (`prompt.With`), so commands ask via `prompterFor(cmd)` and never read `os.Stdin` directly. Never block on a bare stdin read: it ignores ctrl+c.
- `internal/auth`: provider credentials (API key or ChatGPT/Codex OAuth), resolved in order: `auth.json`, env vars, `models.json`, provider default.
- `internal/settings`: `<agent dir>/settings.toml`, what the user changes from inside pi-go (today `default_model`). Patches the raw table so unknown keys survive. The starting model is `--model` > `settings.toml` > `cli.DefaultModel`.
- `internal/config`: generic module loader. A module declares a tagged struct and calls `config.Load("section", &cfg)`; precedence is flag > `PI_GO_<SECTION>_<KEY>` env > `config.toml` > struct defaults, then validator v10. `config` never learns about module fields.

## Conventions that are easy to get wrong

- **Fresh cobra tree per slash command.** `cli.New` takes a *factory* (`commands.All`), and the TUI receives `newTree`, because cobra keeps flag values between runs. Don't hoist command construction into package-level vars.
- **Print through `cmd.OutOrStdout()` / `cmd.ErrOrStderr()`**, never `os.Stdout` or `fmt.Println` in commands. The TUI captures those streams; anything else corrupts the screen.
- **TUI output goes into the transcript** (`m.addEntry`), never `tea.Println`: the session is on the alt screen, where Println prints nothing.
- **Commands that open their own TUI or a browser** must set `Annotations: map[string]string{tui.AnnotationSlash: "false"}` so they are refused and hidden as slash commands (today: `config`, `mcp login`).
- **Import direction:** `commands` imports feature packages; feature packages never import `commands` or each other (except `config`, and the planned `settings` and `trust`). `tui` must not import `cli` or `commands`.
- **Preserve fields you don't own** when writing PI's JSON files (`settings.json`, `mcp.json`): patch a raw map, don't round-trip through a struct. Secrets are redacted by default; only `print-api-key`, `print-bearer-token` and `auth check --credentials` may reveal them.
- Interfaces only at real seams (network, browser, process spawn, clock); no mocks for the filesystem.

## Notes

- `.env` in the repo root only defines a shell alias and is untracked; `.gitignore` ignores the built `/pi-go` binary.

## Rules

- Go with interfaces.