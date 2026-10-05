# pi-go feature checklist

A Go clone of [PI](https://github.com/earendil-works/pi), built to learn harness engineering.
Reference: local PI install at `~/.pi/agent/install/releases/<version>/node_modules/@earendil-works/`
(`pi-ai`, `pi-agent-core`, `pi-tui`, `pi-coding-agent`).

The harness-engineering lessons are concentrated in phases 1-6.

## Phase 1: LLM layer (`pi-ai`)
- [ ] Unified message types: `user`, `assistant` (text, thinking and toolCall blocks), `toolResult`, with images
- [ ] Unified streaming event type: `text_delta`, `thinking_delta`, `toolcall_delta`, `done`, `error`
- [ ] One provider end to end (Anthropic Messages API over SSE), behind a `Provider` interface
- [ ] Partial-JSON parsing of streamed tool-call arguments
- [ ] Tool schema definition and argument validation before execution
- [ ] Usage and cost tracking per message (input, output, cacheRead, cacheWrite)
- [ ] Stop reasons (`stop`, `length`, `toolUse`, `error`, `aborted`) and abort through `context.Context`
- [ ] Thinking levels (off, minimal, low, medium, high, xhigh, max) mapped to provider parameters
- [ ] Prompt-cache handling (cache breakpoints, session ID)
- [ ] Model catalog (context window, cost, input modalities, reasoning support)
- [ ] Message transformation for cross-provider handoff
- [ ] Second provider (OpenAI-compatible completions/responses) to prove the abstraction
- [ ] Auth resolution order: CLI flag, then `auth.json`, then env vars (OAuth optional)

## Phase 2: Agent loop (`pi-agent-core`)
- [ ] Loop: prompt, stream the assistant message, execute tool calls, append results, repeat until no tool calls
- [ ] Event stream: `agent_start/end`, `turn_start/end`, `message_start/update/end`, `tool_execution_start/update/end`
- [ ] Parallel tool execution by default, with per-tool `sequential` override; results persisted in assistant source order
- [ ] `beforeToolCall` (can block) and `afterToolCall` (can rewrite) hooks
- [ ] `terminate: true` tool result to skip the follow-up LLM call
- [ ] Steering queue (injected after the current turn) and follow-up queue (injected when the agent would stop)
- [ ] Abort: cancel the stream and running tools, return queued messages to the editor
- [ ] `transformContext` then `convertToLlm` pipeline (separates app messages from LLM messages)
- [ ] `prepareRequest` and `finishTurn` hooks
- [ ] Agent-level retry with exponential backoff (3 retries, 2s base, 60s cap)

## Phase 3: Built-in tools
- [ ] `read`: `path`, `offset` (1-indexed), `limit`; image files; truncation at 2000 lines or 50KB with a continuation hint
- [ ] `write`: create or overwrite, create parent directories
- [ ] `edit`: `edits[]` of `{oldText, newText}`, each `oldText` unique and non-overlapping, matched against the original file
- [ ] `edit` robustness: BOM and CRLF preservation, tolerant parsing of malformed model args, diff in the result
- [ ] `bash`: optional timeout, stdout+stderr merged, tail truncation, full output spilled to a temp file, kill the whole process group on abort or timeout
- [ ] `grep`, `find`, `ls` (read-only, off by default, with result limits)
- [ ] File-mutation queue so parallel tool calls don't race on the same file
- [ ] Path resolution relative to cwd (`~`, absolute and relative)
- [ ] Per-tool system-prompt snippet and guidelines contributions
- [ ] Tool allowlist and denylist flags (`--tools`, `--exclude-tools`, `--no-tools`)

## Phase 4: System prompt and context
- [ ] Sectioned system prompt: preamble, tools, rules, docs, project context, skills, cwd
- [ ] Rules built dynamically from active tools (e.g. tell the model to use bash for `ls`/`rg` when `grep`/`find`/`ls` are off)
- [ ] Discover `AGENTS.md` and `CLAUDE.md` by walking up from cwd, plus a global one from the agent dir
- [ ] `--system-prompt`, `--append-system-prompt`, `SYSTEM.md` / `APPEND_SYSTEM.md` overrides
- [ ] `@file` and image inclusion in prompts

## Phase 5: Sessions
- [ ] JSONL file per session at `~/.pi-go/agent/sessions/--<cwd-path>--/<ts>_<id>.jsonl`
- [ ] Header line (`version`, `id`, `cwd`, `parentSession`)
- [ ] Tree entries with `id` and `parentId`; the active leaf determines context
- [ ] Entry types: `message`, `model_change`, `thinking_level_change`, `compaction`, `branch_summary`, `custom`, `custom_message`, `label`, `session_info`, `usage`
- [ ] `--continue`, `--resume`, `--session`, `--fork`, `--no-session`, `--name`
- [ ] Rebuild model context from the active branch
- [ ] Branching in place (`/tree`), `/fork` (new file from an earlier user message), `/clone`
- [ ] Session version and migration field in the header
- [ ] Context-edit entries (append-only omission or replacement of an earlier entry)

## Phase 6: Compaction
- [ ] Token estimation, using real provider usage when available
- [ ] Trigger when `contextTokens > contextWindow - reserveTokens` (16384)
- [ ] Find a cut point by walking back until `keepRecentTokens` (20k); never cut at a tool result
- [ ] Structured LLM summary, with the previous summary as input on repeat compactions
- [ ] Handle a split turn when a single user turn exceeds the budget
- [ ] Track read and modified files across compactions
- [ ] `CompactionEntry` with `firstKeptEntryId`; original entries stay on disk
- [ ] Check before a prompt and between turns during a long run
- [ ] Overflow recovery: on a context-overflow error, compact once and retry
- [ ] Manual `/compact [instructions]`
- [ ] Branch summarization when leaving a branch in `/tree`

## Phase 7: Run modes
- [ ] Print mode (`-p`): run once, print the final text
- [ ] JSON mode (`--mode json`): agent events as JSONL
- [ ] RPC mode (`--mode rpc`): JSONL commands on stdin, events on stdout (prompt, steer, follow-up, abort, set model, compact, ...)
- [ ] One `AgentSession` core shared by all modes; no mode has its own agent logic
- [ ] Go SDK equivalent: embeddable `NewSession()` and `Subscribe()`

## Phase 8: Interactive TUI
- [ ] Transcript with streaming text, collapsible thinking blocks, expandable tool output
- [ ] Multi-line editor: Enter to send, Shift+Enter for newline, history, external-editor hand-off
- [ ] Message queueing while running (Enter to steer, Alt+Enter for follow-up, Alt+Up to restore, Esc to abort)
- [ ] Slash-command menu with autocomplete
- [ ] `@file` fuzzy picker and Tab path completion
- [ ] `!cmd` (output goes into context) and `!!cmd` (output stays out)
- [ ] Diff rendering for `edit` results
- [ ] Footer: cwd, session, model, context %, cumulative tokens and cost
- [ ] Markdown rendering and syntax highlighting
- [ ] Image paste (if the terminal supports it)
- [ ] Built-in commands: `/model`, `/thinking`, `/new`, `/resume`, `/name`, `/session`, `/tree`, `/fork`, `/clone`, `/compact`, `/copy`, `/export`, `/settings`, `/reload`, `/hotkeys`, `/quit`
- [ ] Model cycling (Ctrl+P, `--models` globs), thinking cycling (Shift+Tab)
- [ ] Configurable keybindings

## Phase 9: Configuration
- [ ] `~/.pi-go/agent/settings.json` plus project `.pi-go/settings.json` (project overrides global)
- [ ] `models.json` for custom providers and OpenAI-compatible endpoints
- [ ] Model resolver: `provider/id`, fuzzy match, `:thinking` suffix (e.g. `sonnet:high`)
- [ ] Environment-variable key resolution per provider
- [ ] `--list-models`; agent-dir override env var

## Phase 10: Extensibility
- [ ] Skills: `SKILL.md` frontmatter; only name and description in the prompt, full body loaded on demand via `read`; `/skill:name`
- [ ] Prompt templates: Markdown files with `$1`, `$@`, `${1:-default}` substitution
- [ ] Project trust: gate `.pi-go/*` resources behind approval before loading
- [ ] MCP client (stdio and HTTP) with tool exposure control
- [ ] Extensions (event hooks, custom tools, commands); PI loads TypeScript in-process, so in Go use a subprocess with JSON-RPC, WASM, or Yaegi
- [ ] Themes and packages (install from git or npm), can be deferred

## Phase 11: Polish and reliability
- [ ] HTML export of a session
- [ ] Crash log and `/debug` dump
- [ ] Offline mode flag
- [ ] Changelog and update check (optional)

## Skip for a learning project
Telemetry, `/share` and Radius, `/bug` upload, virtual-model routers, `codemode`, Bedrock/Vertex/Cloudflare and other long-tail providers, llama.cpp management, Termux/tmux/Windows docs, image generation and classifier models.

## Suggested order
1. **First working agent:** phases 1-4 plus print mode (Anthropic, loop, read/write/edit/bash, prompt).
2. **Persistence:** phase 5, then phase 6 (the most instructive part).
3. **Interfaces:** JSON and RPC modes (phase 7) before the TUI; they are cheap and force a clean core/UI split.
4. **TUI and beyond:** phase 8 is the biggest time sink (Bubble Tea is the pragmatic choice), then phases 9-10.

## Files worth reading in PI (under `pi-coding-agent/`)
- `dist/core/tools/edit.js`, `bash.js`, `truncate.js`
- `dist/core/system-prompt.js`
- `dist/core/compaction/compaction.js`
- `docs/session-format.md`, `docs/compaction.md`
- `pi-agent-core/README.md` for loop semantics
