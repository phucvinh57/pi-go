# CLAUDE.md

pi-go is a Go clone of the [PI](https://github.com/earendil-works/pi) coding agent, built as a learning project for harness engineering.
## Commands

```sh
go build ./...
go vet ./...
go test ./...
go test ./internal/auth -run TestName   # single test
go run ./cmd/pi-go [flags] [messages...]
go build -ldflags "-X github.com/phucvinh57/pi-go/internal/cli.Version=1.2.3" -o pi-go ./cmd/pi-go   # stamp the version
```

No linter or Makefile is configured. Requires Go 1.27+ (see `go.mod`).

## Architecture

- `cmd/pi-go/main.go` only wires `cli.New(commands.All)` and exits `1` on any error. Typed exit codes (`auth check`: 0/1/2) are planned, not done.
- `internal/cli`: the root `pi` command. It picks interactive vs print mode (`chooseMode`: a TTY on both stdin and stdout and no `-p` means interactive). `cli` is the composition root. `environment.go` reads the agent directory, working directory and `$HOME` once (`hostEnvironment`) and passes them down. `session.go` holds the selected model, reasoning effort and plan-mode state; `conversation.go` owns the agent and session file, built lazily on the first prompt (default model `ollama/qwen2.5-coder:7b`, flags `--provider`/`--model`). `model.go` resolves a `provider/id` into a callable model. `catalog.go` lists selectable models for `/model`. `root.go` adapts `slashcmd` to `tui.Commands`.
- `internal/commands`: thin cobra adapters (parse flags, call a feature package, print). Business rules do not live here. `commands.All()` is the registry (`auth`, `stats`).
- `internal/tui`: the Bubble Tea v2 interactive session (`charm.land/bubbletea/v2`, not the old `github.com/charmbracelet/bubbletea`). It is full screen (alt screen, mouse wheel on): a scrollable `viewport` of the conversation, a rounded input box pinned below it, then a status row and a footer. The conversation is data (`transcript.go`: a list of `entry` values drawn at the current width, so a resize rewraps; `markdown.go` renders assistant text), not scrollback. While a prompt runs, agent events reach `Update` as `eventMsg` and are folded into the transcript. `layout()` runs after every `Update` and sizes the viewport from what `bottom()` draws below it. On quit, `Run` prints the plain transcript to `Options.Out`. Slash commands run TUI built-ins (`slash.go`) first, otherwise the **same cobra tree** as the shell, through the `tui.Commands` interface; `internal/slashcmd` implements it over cobra. `tui` does not import cobra or read the working directory and `$HOME`; `Options.Cwd` and `Options.Home` carry them. Typing `@` at the end of the input opens a file menu (`files.go`); `submit` appends tagged files to the prompt as `<file path=...>` blocks, while the transcript keeps what the user typed. `/model [--default] provider/id` and `/effort [low|medium|high|xhigh|max]` change the model and reasoning effort through interfaces implemented by `cli.session`. `/plan [on|off]` and shift+tab toggle plan mode; a successful plan prompt offers an approval picker. Usage reaches the TUI as `EventStats`; `stats.go` formats it for the footer and `/session`.
- `internal/ai` (models and streaming): every provider fills `Usage` (input excludes both cache counts) and the stream builder prices it in `Finish` via `CalculateCost` from `Model.Cost` (dollars per million tokens), including failed and aborted calls. `Model.ContextWindow` comes from `models.json`, a built-in Codex table, or Ollama `/api/show` (`ai.ContextWindow`); 0 means unknown.
- `internal/tools`: the built-in tools `read`, `bash`, `edit`, `write`. Each is a `Tool` (a `Spec` plus `Execute(ctx, rawJSON, onUpdate)`); a returned error is the message the model sees. Knows nothing about `ai`; the agent loop adapts `Spec` to `ai.Tool`. `tools.Core(cwd)` returns all four.
- `internal/agent`: the loop. `Agent.Prompt` (or `PromptWith`, which also emits text/tool events) appends a user message, calls the configured model, runs requested tools, and repeats until a reply has no tool calls (max 25 model calls). A failed prompt rolls the conversation back, but not its usage: `Agent.Stats()` (`stats.go`) keeps totals of every model call, and context size is estimated PI-style. `EventStats` is sent after each reply, failed call and rollback; `Stats.SubscriptionCost` tracks calls paid by subscription. `Config.Recorder` receives messages as they are created and model changes before a call. The agent also builds the system prompt from active tools. `Agent.SetPlanMode` swaps in read-only tools and `Config.PlanPrompt`; hidden tool calls return an error result. It is the one feature package that imports `ai` and `tools`.
- `internal/prompt`: how a command asks the user something (`Prompter`: `Select` a list, `Input` a line, optionally secret). `Terminal` draws inline and reads raw keys, so ctrl+c/esc always cancel (`ErrCancelled`); `Lines` reads piped stdin. The TUI puts its own on-screen `Prompter` in the command's context (`prompt.With`), so commands ask via `prompterFor(cmd)` and never read `os.Stdin` directly. Never block on a bare stdin read: it ignores ctrl+c.
- `internal/auth`: provider credentials (API key or ChatGPT/Codex OAuth), resolved in order: `auth.json`, env vars, `models.json`, provider default. `auth.Store` is built on an agent directory (`auth.NewStore(dir)`); its methods do the reading and writing. `Credential.OAuth` marks a subscription, whose cost is shown as `(sub)` because it is not a bill.
- `internal/modelsfile`: reads `<agent dir>/models.json` (read only), a leaf package with no internal imports. `Read(dir)` gives a `File` with `APIKey`, `BaseURL`, `Models`, `Model` (an `Entry`: `contextWindow`, `maxTokens` and `cost` with `input`/`output`/`cacheRead`/`cacheWrite`, $ per million tokens) and `ProvidersOf`. `auth` uses it for keys and base URLs, and `cli` for model limits and prices.
- `internal/slashcmd`: runs the shell's cobra commands for the interactive session, lists the ones it may offer and completes their arguments (`Commands.Run`/`List`/`Complete`: subcommands, flags, flag values). It owns `AnnotationSlash` and `AnnotationValues`.
- `internal/session`: saves conversations as PI-style JSONL (`message` and `model_change` entries) under `<agent dir>/sessions/--<cwd>--/` (header, then entries linked by `parentId`; messages are pi-go's `ai.Message`). `Writer` implements `agent.Recorder`, writes nothing until there is a user or assistant message, appends one line per entry, and never fails a prompt (`cli` reports the first write error once; `--no-session` turns saving off). `Load`/`LoadAll` skip damaged lines. `Summarize` counts recorded usage into a `Summary`; `session/report` renders a self-contained HTML page with inline SVG charts.
- `internal/browser`: `Open`, a variable so tests never launch a browser.
- `internal/settings`: `<agent dir>/settings.toml`, the user's saved default model. `settings.NewStore(dir)` is built on the agent directory. Writes patch the raw table so unknown keys survive. The starting model is `--model` > `settings.toml` > `cli.DefaultModel`.
- `internal/config`: generic module loader. A module declares a tagged struct and calls `config.Load("section", &cfg)`; precedence is flag > `PI_GO_<SECTION>_<KEY>` env > `config.toml` > struct defaults, then validator v10. `config` never learns about module fields.

## Conventions that are easy to get wrong

- **Fresh cobra tree per slash command.** `cli.New` takes a *factory* (`commands.All`), and the TUI receives `newTree`, because cobra keeps flag values between runs. Don't hoist command construction into package-level vars.
- **Print through `cmd.OutOrStdout()` / `cmd.ErrOrStderr()`**, never `os.Stdout` or `fmt.Println` in commands. The TUI captures those streams; anything else corrupts the screen.
- **TUI output goes into the transcript** (`m.addEntry`), never `tea.Println`: the session is on the alt screen, where Println prints nothing.
- **Flag values for slash completion** go in the flag's `slashcmd.AnnotationValues` annotation (`cmd.Flags().SetAnnotation`), not `RegisterFlagCompletionFunc`: cobra keeps those in a global map keyed by flag, and the session builds a fresh tree per keystroke. Built-ins complete their arguments through `builtin.complete` (`tui/complete.go`).
- **Commands that open their own TUI or a browser** must set `Annotations: map[string]string{slashcmd.AnnotationSlash: "false"}` so they are refused and hidden as slash commands (today: `stats`, which opens the report; later `config`, `mcp login`).
- **Import direction:** `commands` imports feature packages; feature packages do not import `commands` or each other (except leaf packages such as `config`, `browser` and `modelsfile`, and the planned `trust`; `session` imports `ai` for messages, and `session/report` imports `session`). `tui` imports only `prompt` among them: it must not import `cli`, `commands`, `slashcmd` or cobra. `cli` joins features; it reads the machine once (`hostEnvironment`) and passes the agent dir and cwd down. Packages below `cli` take a directory (`auth.NewStore`, `settings.NewStore`, `modelsfile.Read`) and do not call `config.AgentDir()` themselves; `commands` is the exception at its edge.
- **Preserve fields you don't own** when writing PI's JSON files (`settings.json`, `mcp.json`): patch a raw map, don't round-trip through a struct. Secrets are redacted by default; only `print-api-key`, `print-bearer-token` and `auth check --credentials` may reveal them.
- Interfaces only at real seams (network, browser, process spawn, clock); no mocks for the filesystem.

## Notes

- `.env` in the repo root is untracked and keeps the local `pi-go` shell alias. `.gitignore` ignores the built `/pi-go` binary and `/.venv/`.

## Rules

- Go with interfaces.
