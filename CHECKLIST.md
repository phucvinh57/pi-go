# pi-go feature checklist

A Go clone of [PI](https://github.com/earendil-works/pi), built to learn harness engineering.
Reference: local PI install at `~/.pi/agent/install/releases/<version>/node_modules/@earendil-works/`
(`pi-ai`, `pi-agent-core`, `pi-tui`, `pi-coding-agent`).

Already done: auth, config, the cobra command tree and the TUI shell. Everything below is the agent behind them.

## 1. Talk to a model
- [x] Common message and streaming types shared by all providers
- [x] Ollama provider (local, free; build this first)
- [x] openai-codex provider (second, to prove the types work across providers)
- [x] Model resolution (`provider/id`) using the existing auth

## 2. Agent loop
- [ ] Prompt the model, run the tools it asks for, send results back, repeat until it answers
- [ ] Events the UI can subscribe to
- [ ] Abort and retry

## 3. Core tools
- [x] `read`
- [x] `write`
- [x] `edit`
- [x] `bash`

## 4. System prompt
- [ ] Prompt built from the active tools
- [ ] Load `AGENTS.md` / `CLAUDE.md` from the project

**Milestone:** `pi-go -p "..."` works end to end (steps 1-4, Ollama only).

## 5. Sessions
- [ ] Save each conversation to a file
- [ ] Continue and resume
- [ ] Branch and fork

## 6. Compaction
- [ ] Summarize older messages when the context is nearly full
- [ ] Manual `/compact`

## 7. Other run modes
- [ ] JSON mode (events as lines)
- [ ] RPC mode (another program drives the agent)

## 8. Connect the TUI
- [ ] Streaming text and tool output in the transcript
- [ ] Send a message while the agent is working; Esc to stop it
- [ ] `/model`, `/compact` and other session commands

## 9. Extras
- [ ] Settings files
- [ ] Skills and prompt templates
- [ ] MCP tools exposed to the agent
- [ ] Extensions, themes, HTML export

## Skipped
Telemetry, `/share`, long-tail providers, and anything else that doesn't teach harness engineering.
