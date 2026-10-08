# Built-in PII and credential masking plan

Status: proposed; implementation has not started.

## Goal

Detect and mask sensitive information locally before pi-go sends content to AI providers. Cover emails, phone numbers, usernames, passwords, Visa/Mastercard numbers, API keys, secret keys, and private keys.

Use a **local open-weight span detector plus deterministic credential rules**. A model alone cannot reliably recognize arbitrary passwords or secrets. Masking is best-effort detection, not a guarantee against all data leakage.

## Model candidates — verify before implementation

1. [urchade/gliner_multi_pii-v1](https://huggingface.co/urchade/gliner_multi_pii-v1): initial candidate for local multilingual PII span extraction with configurable labels.
2. [gliner-community/gliner-pii-base-v1.0](https://huggingface.co/gliner-community/gliner-pii-base-v1.0): alternative to benchmark for accuracy and CPU latency.

These are research candidates, not verified selections. Current model cards could not be fetched during planning. Confirm that the repositories are available, review checkpoint and dependency licenses, supported labels/languages, inference APIs, and deployment requirements. Pin the selected checkpoint revision and dependencies. Do not assume either model reliably detects all requested credential types.

Start with local Python inference behind a loopback-only service. The policy and enforcement live in Go; inference requires a separately installed local runtime. Investigate single-binary/ONNX deployment later, after verifying checkpoint compatibility.

## Repository findings

- `internal/agent/agent.go`: `complete()` sends the full conversation to the selected provider. Protect all turns, not just the latest user message: file contents and shell output return through tool results.
- `internal/cli/conversation.go`: constructs the agent and session recorder; suitable configuration/wiring point.
- `internal/cli/session.go`: manages provider/model changes and session behavior. Protection must persist across changes.
- `internal/ai/types.go`: tool arguments and schemas use JSON. Preserve JSON validity and tool-call linkage while sanitizing content.
- `internal/config/config.go`: existing module configuration loader supports TOML and environment overrides.
- Conversation session files are already written by the implementation, although the README still describes sessions as unimplemented. Outbound masking does not automatically protect local transcripts.

## Security boundary and policy

Protect every provider-bound content request, including system prompts, conversation history, tool results, tool-argument JSON, and content-bearing tool descriptions/schema fields.

- Operate on a sanitized copy; do not modify source files or the original conversation just to mask an outbound request.
- Preserve protocol identifiers and linkage. Provider authentication headers still need real credentials; mask credentials embedded in prompt content, not authentication required for transport.
- Keep stable, session-scoped placeholders such as `[EMAIL_1]`, `[PASSWORD_1]`, and `[API_KEY_1]`.
- Keep any original-value mapping only in memory. Do not log matched values, mappings, or request bodies.
- In hybrid mode, detector failure, timeout, invalid spans, or incomplete scanning blocks transmission. No silent fallback to unmasked requests or rules-only mode.
- Offer explicit `hybrid`, `rules-only`, and `off` modes, with their protection differences documented.
- Do not automatically expand placeholders into model-generated shell commands or file writes.
- Local UI/history and existing session files may still contain originals. Clearly disclose this separately from outbound protection.
- This feature is not a shell sandbox: arbitrary tool execution, obfuscated/encoded secrets, and detection misses remain risks.

## Implementation steps

### 1. Evaluate and pin a detector

Proposed files:

- `docs/privacy.md`
- `testdata/privacy/`
- Benchmark tooling under `scripts/privacy/`

Tasks:

- Verify the candidate model cards, licenses, labels, language support, and actual inference APIs.
- Create synthetic fixtures for prompts, source code, `.env` files, JSON, logs, and multilingual text. Do not commit real credentials or personal data.
- Measure recall, false positives, memory use, and CPU latency separately for each requested category.
- Include ambiguous usernames/passwords, secrets without contextual labels, ordinary code identifiers, UUIDs, hashes, and public example values.
- Select and pin a checkpoint based on results. Record known gaps instead of claiming universal coverage.

### 2. Build the Go masking engine

Proposed files:

- `internal/privacy/detector.go`
- `internal/privacy/rules.go`
- `internal/privacy/masker.go`
- Corresponding tests

Tasks:

- Define a detector interface returning typed spans and a clear offset convention.
- Combine model spans with deterministic rules for:
  - Email and phone candidates.
  - Payment-card candidates with Luhn validation; document that this does not prove a card is real or active.
  - Known provider credential formats.
  - PEM private-key blocks.
  - Authorization headers and credential-bearing URLs.
  - Contextual assignments such as `password=...`, JSON secret fields, and environment variables.
- Use entropy only as supporting evidence, not a blanket rule for all hashes and identifiers.
- Resolve overlapping spans conservatively, validate span boundaries, and handle UTF-8 correctly.
- Assign stable session-scoped placeholders, handle literal-placeholder collisions, and avoid retaining unbounded sensitive data in caches.

### 3. Add local inference and configuration

Proposed files:

- `internal/privacy/client.go`
- `internal/privacy/config.go`
- `scripts/privacy/server.py`
- Pinned Python dependency/setup files

Tasks:

- Load a `[privacy]` module configuration using the existing configuration loader.
- Provide explicit `hybrid`, `rules-only`, and `off` modes.
- Restrict inference to loopback; reject redirects and avoid proxy-based forwarding. Validate endpoint behavior rather than merely assuming that a configured address is local.
- Disable request logging and return value-free errors.
- Bound request/response sizes and use cancellation and timeouts.
- Scan long input in overlapping chunks; never silently truncate. Test entities crossing chunk boundaries and ensure complete coverage.
- Download model weights only during explicit setup. Normal inference stays local and does not require a hosted inference API.
- Fail closed on unavailable inference, malformed responses, invalid offsets, and incomplete coverage in hybrid mode.

### 4. Enforce protection on every outbound content path

Proposed changes:

- `internal/agent/agent.go`
- `internal/cli/conversation.go`
- New `internal/privacy/context.go`

Tasks:

- Build sanitized copies of AI contexts.
- Scan system prompts, all message text/thinking content, tool results, tool arguments, and content-bearing tool declarations.
- Traverse JSON structurally rather than replacing raw serialized text in a way that could break escaping or syntax. Define behavior for sensitive object keys and non-string scalar values as well as string values.
- Preserve tool-call IDs, names, and other required protocol structure; do not send sensitive data through metadata fields as an accidental bypass.
- Ensure privacy failures stop the request before invoking the provider.
- Reapply protection for each tool-loop iteration, continuation, retry, and model/provider change.
- Audit other outbound content paths before declaring coverage complete.

### 5. Define safe tool and local-record behavior

Proposed changes:

- `internal/agent/agent.go`
- `internal/cli/session.go`
- `docs/privacy.md`

Tasks:

- Never automatically restore original secrets into model-generated commands, edits, or writes.
- Reject unresolved protected placeholders in executable tool arguments with a clear, value-free error instead of silently executing broken or unsafe operations. Define the exact scope carefully so legitimate discussion of placeholders still works.
- Surface detection counts/categories without exposing matched values.
- Document the trade-off: editing masked files or using masked paths may need a later, separately designed local substitution workflow.
- Clearly disclose that local session files, UI output, shell output, and source files are outside the initial outbound-only masking guarantee.
- Review error paths and debug logging for accidental inclusion of original content.

### 6. Verify end-to-end behavior and document setup

Proposed changes:

- Extend `internal/agent/agent_test.go`
- Add privacy, configuration, and HTTP request-capture tests
- Update `README.md` and `docs/privacy.md`

Tests and acceptance criteria:

- Synthetic secrets detected by the fixtures do not appear in captured provider request bodies.
- Protection covers initial prompts, history, system prompts, `read` output, `bash` output, tool arguments, repeated turns, and provider switches.
- Detector outage, cancellation, malformed responses, incomplete scans, and invalid spans cause zero subsequent outbound content requests for that attempt.
- JSON remains valid; tool-call linkage remains intact.
- Unicode, long input, chunk boundaries, overlapping detections, repeated values, and placeholder collisions behave predictably.
- Ordinary code has measured false-positive coverage, not just positive detection tests.
- Local source files and original conversation values are not mutated by outbound sanitization.
- Provider authentication is preserved without leaking credentials into content or logs.
- Run `go test ./...`, `go test -race ./...`, and the separate model benchmark.
- Decide the default mode and first-run setup experience only after measuring detection quality, runtime availability, and latency. Do not enable hybrid mode by default without a working setup path and clear failure behavior.

## Decisions to resolve during implementation

- Which checkpoint passes license review and the category-by-category benchmark?
- Which languages need first-class support, especially English and Vietnamese?
- What CPU latency and memory budget are acceptable per agent turn?
- Which contextual patterns justify masking usernames and unlabeled secrets without destroying code usefulness?
- How should numeric JSON secrets be represented while maintaining schema compatibility? Blocking may be safer than silently changing required types.
- How should first-run setup and explicit protection opt-out work?
- Should a subsequent feature redact or disable local session persistence, separately from outbound masking?

## Suggested starting point when resuming

1. Recheck the working tree and current agent/session flow.
2. Fetch and verify the candidate model cards and licenses.
3. Create synthetic evaluation fixtures and run a small local model benchmark.
4. Implement the masking engine and fail-closed agent/provider boundary with fake detectors before wiring real inference.
5. Add the local model service, configuration, integration tests, and user documentation.

Only this planning document was added for the handoff; no masking implementation has been made.
