# Structure

How the pi-go command surface (`install`, `remove`, `update`, `list`, `config`, `auth`, `mcp`) is organized. Flat packages, one per feature. No layers, aggregates, repositories or events: these commands are mostly reading and writing JSON files, and the real design effort belongs in phases 1-6 of [CHECKLIST.md](../CHECKLIST.md).

Derived from [internal/commands/](../internal/commands/), [internal/auth/](../internal/auth/), `pi-go --help` and PI's docs (`packages.md`, `mcp.md`, `settings.md`, `security.md`, `cli.md`).

## Layout

```
cmd/pi-go/main.go         wires everything, maps errors to exit codes
internal/
  cli/                 root `pi-go` command: picks interactive vs print mode (agent run, later)
  tui/                 interactive session (Bubble Tea); runs slash commands through the cobra tree
  commands/            cobra adapters: parse flags, call a package, print. No rules here.
  auth/                provider credentials: log in (API key or ChatGPT OAuth), resolve, check readiness, print key/token
  tools/               built-in agent tools: read, bash, edit, write (JSON args in, text out)
  packages/            package sources, install/remove/list/update, resource filters
  mcp/                 MCP server config, validation, list, OAuth login/logout
  config/              generic loader: modules declare a tagged struct, `config.Load(section, &cfg)` reads `[section]` of `config.toml` (viper) and validates it (validator v10)
  settings/            read-modify-write JSON that keeps fields it does not own
  trust/               project trust decision + trust.json
```

Rules of thumb:
- `commands` imports feature packages. Feature packages never import `commands` or each other, except `config`, `settings` and `trust`, which any of them may use.
- `tui` imports cobra but not `cli` or `commands`; it receives a factory that builds a fresh root command. `cli` and `commands` may import `tui` (only for `tui.AnnotationSlash`).
- Commands print through `cmd.OutOrStdout()` / `cmd.ErrOrStderr()`, never `os.Stdout` or `fmt.Println`. The interactive session captures their output into the transcript; anything written elsewhere corrupts the screen.
- A command that opens its own TUI or a browser sets `Annotations: {tui.AnnotationSlash: "false"}` so it is refused (and not suggested) as a slash command. Today: `config`, `mcp login`.
- Interfaces only at real seams (network, browser, spawning a process, the clock). Everything else is a plain function or struct.
- Tests set `PI_GO_CODING_AGENT_DIR` to a temp dir and use `httptest`. No fake repositories.

## Interactive session

`pi-go` in a terminal (stdin and stdout are TTYs, no `-p`) opens the session in `internal/tui`; positional args become the first prompt. `pi-go -p ...`, or either end not being a terminal, answers once and exits; piped stdin is prepended to the prompt. Subcommands (`pi-go auth check`) never open a session.

Inside the session, `/<name> ...` runs TUI built-ins (`/quit`, `/clear`, `/help`) first, otherwise the same cobra command as the shell, on a fresh command tree, with its output shown in the transcript. A name that matches no subcommand is rejected before execution, because it would otherwise fall through to the root command and nest a session. Tab completes command names from the tree; flags are not completed.

## Command to package

| Command | Package | Writes |
|---|---|---|
| `install`, `remove`/`uninstall`, `list`, `config` | `packages` | `settings.json` (global, or `.pi-go/settings.json` with `-l`) |
| `update` (default / `pi` / `self`) | `packages` (`selfupdate.go`) | `~/.pi-go/agent/install/releases/<v>`, `current-version` |
| `update --extensions`, `update <source>` | `packages` | package cache |
| `update --models` | `auth` for now, split out when the model catalog grows | `models-store.json` |
| `auth login`, `auth check`, `print-api-key`, `print-bearer-token` | `auth` | `auth.json` (OAuth refresh only) |
| `mcp add`, `remove`, `list` | `mcp` | `mcp.json` (global or `.pi-go/mcp.json`) |
| `mcp login`, `logout` | `mcp` | `mcp-auth.json` |

`pi update` is the one command that spans features. Keep its orchestration in `commands/update.go`: pick the targets from the flags, run each, collect results, exit non-zero if any failed.

## Module settings

Each module owns its schema and loads it from its own `config.toml` table. `config` never learns about module fields.

```go
type Config struct {
    BaseURL string `mapstructure:"base_url" validate:"omitempty,url"`
}
cfg := Config{BaseURL: "http://localhost:11434"} // defaults live in the struct
err := config.Load("auth", &cfg)                 // [auth] in config.toml
```

Precedence: flag (`config.WithFlags`, matched by field key) > `PI_GO_<SECTION>_<KEY>` env > `config.toml` > struct defaults. Validation errors name the key and rule, never the value. Credentials stay in `auth.json`, not `config.toml`.

## Rules worth keeping

1. **Parse once at the boundary.** Small types that validate on construction and carry their own identity:
   - `packages.Source`: `npm:` / `git:` / URL / local path. Identity is the npm name, the git URL without the ref, or the absolute local path (resolved against the settings file, not the cwd). `install foo@2` after `foo@1` replaces the entry.
   - `mcp.ServerName`: letters, digits, `_`, `-`. `my-srv` and `my_srv` are the same server; a differently spelled duplicate is rejected.
   - MCP config: exactly one of stdio (`command`) or HTTP (`url`). `--bearer-token-env-var X` just stores `Authorization: Bearer ${X}` as a normal header.
2. **Never drop fields you do not own.** `settings.json` also holds theme, default model and so on; `mcp.json` has keys like `autoEnableCodemode`. Edit through `settings` (raw map, patch the keys you own, write back). Add a round-trip test: unknown fields survive.
3. **Secrets are redacted by default.** Credential type with a redacting `String()`/`MarshalJSON()`. Only `print-api-key`, `print-bearer-token` and `auth check --credentials` reveal it. `Status` and JSON output never contain it (already true today).
4. **Ordered credential sources.** Resolve through an ordered slice of sources: `--api-key`, `auth.json`, env vars, `models.json`, provider default. Adding OAuth or `!command` keys means adding a source, not editing the resolver.
5. **Typed exit codes.** `auth check`: `0` ready, `1` not ready, `2` invalid. `mcp list`: `1` on an invalid entry or an enabled server not connected. Errors carry an exit code; `main` reads it instead of always exiting `1`.
6. **Project scope goes through `trust`.** One call, `trust.Allowed(dir, override)`, order: `--approve`/`--no-approve`, saved decision (closest parent in `trust.json`), `defaultProjectTrust`. Without a TTY, `ask` behaves like `never`. Untrusted: reads skip the project scope, writes with `-l` fail.
7. **Do not resolve MCP `${VAR}` / `!command` values** when adding or listing config. Store them as written; resolve only when connecting.
8. **Fetch before save.** `install` downloads and validates first, then writes `settings.json`. A failed install leaves settings untouched. `remove` edits settings first, then deletes the cache best-effort.

## Gaps in the current stubs

| Command | Stub has | PI has |
|---|---|---|
| project-aware commands | no trust flags | `-a/--approve`, `-na/--no-approve` |
| `update` | `--all`, `--extensions`, `--models` | also `--self`, `--extension <src>`, `--force`; `self`/`pi` aliases |
| `auth` | `login <provider>` (API key, or `openai-codex` OAuth); `check` with `--provider --model --json` | also `print-api-key`, `print-bearer-token`, `--credentials`, `--no-refresh`, `--min-expiry`; exit codes `0/1/2` |
| `mcp add` | `--local --url --env --header` | also `--cwd`, `--bearer-token-env-var`, `--oauth-*`, `--exposure`, `--description` |
| `mcp login` | none | `--timeout <seconds>` (default 300) |
| `auth` providers | `openai`, `ollama` | many; the Go version deliberately keeps an approved list |

## Suggested order

1. **Exit codes in `main`**, then `auth`: add a readiness state, `print-api-key`, and split the Ollama probe from `Check`. The package already works, so this is a small refactor.
2. **`settings` + `trust`**, with the round-trip test.
3. **`packages`**: `list`, `install`, `remove` with local and git sources first, npm later.
4. **`mcp`**: `add`, `remove`, `list`, then `login`/`logout`.
5. **`update`**: `--models`, then packages, then self-update (riskiest, last).
6. **`config`** TUI together with phase 8, since it needs the same TUI stack.

## Open questions

1. `auth check` exit code `2`: PI documents it but not the boundary with `1`. I assume `2` means a credential exists but is unusable (failed refresh, malformed) and `1` means none found. Check `dist/cli/auth-check.js` in the local PI install.
2. `install -l` in an untrusted project: PI docs are silent. Assumed to fail unless `--approve`.
3. `update` with several targets: assumed to run all, report all, and exit non-zero on any failure; self-update runs last.
4. `auth check` with no flags: the stub checks all approved providers; PI requires `--provider` or `--model`. Keep the stub's behavior?
5. npm sources: shell out to `npm` (PI honors an `npmCommand` setting) or ship git and local only at first?
6. `--models` refresh needs a home once the model catalog (CHECKLIST phase 1/9) exists. Until then it can live in `auth`'s neighbor or its own small `models` package.
