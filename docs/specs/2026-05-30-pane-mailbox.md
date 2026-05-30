# Pane Mailbox Design

Date: 2026-05-30. Issue: LAB-1990.

## Motivation

Agents need an amux-native way to leave structured messages for panes without
typing into another pane's PTY. `send-keys` is correct for terminal input, but
it is the wrong primitive for coordination: it can corrupt prompts, it has no
delivery state, and it forces agents to parse terminal text to distinguish work
from notification noise.

The primitive in this spec is a **pane-addressed, session-scoped,
server-owned mailbox**. It belongs to amux, not to orca or any agent runtime.
amux stores messages, tracks per-recipient read/ack state, emits mailbox events,
and exposes a generic CLI. External tools can build policy on top of it, but the
mailbox itself stays a small transport and state primitive.

## Design Principles

1. **Out of band means no PTY writes.** Delivering a message never writes bytes
   to a pane's terminal. A pane or user must explicitly read the message with
   `amux msg read`.
2. **Session-local and server-owned.** A mailbox is scoped to one amux session
   and lives in the server's in-memory session state. It is checkpointed with the
   session, but it does not use an external database or background service.
3. **Panes are the addressable identity.** Users address senders and recipients
   with the same pane references used elsewhere: pane name, numeric ID, or
   unambiguous prefix. The resolved recipient set is frozen at send time.
4. **Capture and rendering expose summaries only.** Full message bodies are
   available only through `amux msg read`. Capture JSON, events, and status-line
   badges expose unread counts and safe summaries, not bodies.
5. **No orchestration policy.** The mailbox knows nothing about tasks, agents,
   Linear issues, PRs, retries, escalation, or "done" semantics.

## Primitive

Each session owns one `Mailbox` instance:

```go
type Mailbox struct {
    NextSeq    uint64
    Messages   map[MessageID]*Message
    Deliveries map[uint32]map[MessageID]*DeliveryState // recipient pane ID -> message ID
    Threads    map[ThreadID][]MessageID
}
```

This is conceptual shape, not an implementation requirement. The implementation
should live behind a small package boundary, for example `internal/mailbox`, and
the server should mutate it on the session event loop so command handlers,
events, waits, and checkpointing see a consistent state.

### Message Identity

Message IDs are opaque, monotonic within the session, and stable across
checkpoint/restore:

```text
msg-000001
msg-000002
```

The sequence counter is checkpointed with the mailbox. IDs are not security
tokens. They are handles for `read`, `ack`, thread references, and event
correlation.

### Addressing

A message has one sender pane and one or more recipient panes:

```go
type PaneAddress struct {
    ID   uint32
    Name string
    Host string
}
```

The sender is required. `msg send` resolves it from `--from` or from the command
actor pane when available. If neither exists, the command fails instead of
creating an anonymous message.

Recipients are resolved at send time. If any recipient ref is unknown,
ambiguous, duplicated in a way that changes intent, or points at a dead pane,
the entire send fails and no message is stored. This avoids partial delivery
surprises.

Recipient snapshots store both ID and name. The ID is the delivery key while the
server is running; the name is retained for display and diagnostics if the pane
is later closed.

### Topics And Groups

Topics and groups are optional labels on the message:

```go
Topics []string
Groups []string
```

They do not carry policy in v1. They exist for filtering `inbox`, `watch`, and
`wait msg`, and for future tooling to agree on routing conventions without
changing the core data model.

Valid topic and group names should be short ASCII identifiers:
`[A-Za-z0-9][A-Za-z0-9._/-]{0,63}`. Invalid names fail loudly. amux should not
ship built-in meanings for names like `task`, `review`, `orca`, or `a2a`.

If a later bead adds first-class group membership, it should be additive:
`msg send --group reviewers` can expand to panes then, while messages that only
carry the `reviewers` label remain valid.

### Threads And Replies

Thread IDs group messages without defining workflow semantics:

```go
type ThreadID string

type Message struct {
    ID        MessageID
    ThreadID  ThreadID
    InReplyTo MessageID
    Replies   []MessageID
}
```

If `msg send --reply-to msg-000123` is used, the new message inherits the root
thread ID from `msg-000123`, stores `InReplyTo`, and appends itself to the
parent message's `Replies`. If no thread is provided, the message ID becomes the
thread ID.

amux does not infer completion from replies. A reply is just another message
with links.

### Subject, Body, Parts

A message has a short subject and one or more body parts:

```go
type Message struct {
    Subject string
    Parts   []MessagePart
}

type MessagePart struct {
    Name        string
    ContentType string // default text/plain; charset=utf-8
    Encoding    string // utf-8 or base64
    Bytes       []byte
    Size        int
}
```

v1 should optimize for one `text/plain; charset=utf-8` part because that covers
CLI and agent use. The `Parts` shape prevents a future attachment or structured
payload feature from needing a message format migration.

Subjects are single-line UTF-8 and should reject control characters. Bodies may
contain newlines and tabs. Other control bytes require base64 encoding.

### Structured Metadata

Each message can carry an optional JSON object:

```go
Metadata map[string]json.RawMessage
```

Metadata is generic and uninterpreted by amux. It is available from `msg read
--format json`, not from capture summaries or rendering. Keys starting with
`amux.` are reserved for future amux-owned metadata. Unknown keys are preserved
through checkpoint/restore.

### Delivery, Read, Ack, Reply State

Delivery state is per recipient:

```go
type DeliveryState struct {
    MessageID    MessageID
    Recipient    PaneAddress
    DeliveredAt  time.Time
    ReadAt       time.Time
    AckedAt      time.Time
    AckStatus    string // "", "ok", "error", "seen", or another generic token
    AckNote      string
    LastEventSeq uint64
}
```

`read` and `ack` are separate:

- `read` means the full body was returned by `msg read` for that recipient.
- `ack` means the recipient explicitly acknowledged the message.
- `reply` state lives on messages (`InReplyTo`, `Replies`, `ThreadID`), not in
  delivery state.

`msg read --peek` returns the body without setting `ReadAt`. `msg ack` may be
called before `read`; that records an ack without implying the body was read.
Repeating an identical ack is idempotent and reports the existing state. Acking
an unknown message, a message not delivered to the target pane, or an invalid
recipient is a non-zero error.

### Timestamps

Messages store:

- `CreatedAt`
- `UpdatedAt`
- `ExpiresAt` (optional)
- per-recipient `DeliveredAt`, `ReadAt`, and `AckedAt`

Use UTC and serialize as RFC3339Nano in JSON. The server clock is the only time
source; clients do not supply authoritative timestamps.

### Size And Security Limits

Mailbox limits should be intentionally smaller than the 16 MB wire-message cap:

| Limit | Recommended v1 value |
| --- | ---: |
| Subject | 512 bytes |
| One body part | 64 KiB |
| Total message body | 256 KiB |
| Metadata JSON | 16 KiB |
| Ack note | 4 KiB |
| Recipients per message | 128 |
| Stored messages per session | 10,000 |
| Stored mailbox bytes per session | 64 MiB |

When a cap is reached, new writes fail loudly with a specific error. The server
should also prune messages that are fully acked and older than a retention
window once caps are approached. Unread messages should not be pruned before
expired messages and fully acked messages.

Security boundaries:

- Same-user access to the amux Unix socket is the trust boundary. The mailbox is
  not an ACL system.
- Message bodies are never emitted in `amux capture`, `amux events`,
  `amux msg watch`, status lines, logs, or error strings.
- `msg read --format json` JSON-escapes bodies. Plain text output may print the
  body for human convenience, but it writes to the caller's stdout, not to a
  pane PTY.
- Metadata is data, not instructions. amux does not execute hooks based on it.
- Message delivery never injects prompts, keystrokes, shell commands, or escape
  sequences into a recipient pane.

## CLI Surface

The namespace is `amux msg <subcommand>` plus `amux wait msg`.

All commands support `--format json` where structured output is useful. Text
output should be concise, but JSON is the stable automation contract.

### `msg send`

```bash
amux msg send --from pane-1 --to pane-2 \
  --subject "Review ready" \
  --body "Please review the latest diff."

amux msg send --from pane-1 --to pane-2,pane-3 \
  --topic review --group backend \
  --metadata '{"priority":"normal"}' \
  --body-file /tmp/message.txt

printf 'body from stdin\n' | amux msg send --from pane-1 --to pane-2 --subject "stdin"

amux msg send --from pane-2 --to pane-1 --reply-to msg-000123 --body "Ack, looking now."
```

Rules:

- `--from` is required unless the command has an actor pane.
- At least one `--to` recipient is required.
- `--body`, `--body-file`, and stdin are mutually exclusive body sources.
- Sending with no resolvable recipients, an empty body, invalid metadata,
  invalid topic/group names, or an oversize payload exits non-zero.
- On success, output includes the message ID and frozen recipient list.

### `msg inbox`

```bash
amux msg inbox pane-2
amux msg inbox pane-2 --unread
amux msg inbox pane-2 --topic review --limit 20 --format json
```

`inbox` lists message summaries for one recipient pane. It does not include full
bodies or arbitrary metadata values. The summary fields are:

- message ID
- sender pane ID/name
- recipient pane ID/name
- subject
- topics/groups
- thread ID and `in_reply_to`
- created/delivered/read/ack timestamps
- body size and part count

An unknown pane or invalid filter exits non-zero. An empty inbox is a successful
empty result.

### `msg read`

```bash
amux msg read msg-000123 --for pane-2
amux msg read msg-000123 --for pane-2 --peek --format json
```

`read` is the only command that returns full bodies. It marks the delivery read
for the target recipient unless `--peek` is set.

If `--for` is omitted and the command has an actor pane, the actor pane is used.
Otherwise the command fails. Reading a message that was not delivered to the
target pane fails. This keeps read state meaningful even though the same local
Unix-socket user ultimately controls the session.

### `msg ack`

```bash
amux msg ack msg-000123 --for pane-2 --status ok
amux msg ack msg-000123 --for pane-2 --status error --note "Need more context."
```

`ack` records explicit recipient acknowledgement. `--status` is a generic token;
amux should document `ok`, `error`, and `seen`, but should not assign task
semantics to them. `--note` is optional and bounded by the ack-note size limit.

### `msg watch`

```bash
amux msg watch pane-2
amux msg watch pane-2 --topic review --format json
```

`watch` streams mailbox lifecycle events as NDJSON. It sends summary-only
events, never full bodies. By default it first emits current unread summaries
matching the filter, then streams future matching events. A `--no-initial` flag
can suppress the initial snapshot for consumers that already have a cursor.

### `wait msg`

```bash
amux wait msg pane-2 --timeout 5m
amux wait msg pane-2 --topic review --after msg-000123 --timeout 30s --format json
```

`wait msg` blocks until an unread message matching the filter exists for the
target pane. It returns a summary, not the body. The recipient must call
`msg read` to fetch the full message.

Default wait semantics:

1. Check existing unread messages first. If one matches, return immediately.
2. Otherwise subscribe to mailbox events and wait for the next matching
   delivery.
3. On timeout, exit non-zero with a timeout error.
4. If the target pane exits while waiting, exit non-zero with a pane-exited
   error.

`--after` accepts a message ID or event sequence. This lets a caller avoid
re-processing older unread messages while still avoiding races between
`inbox` and `wait`.

## Server And Event Behavior

### Lifecycle

Message lifecycle:

```text
send accepted
  -> message stored
  -> delivery records created
  -> message-delivered events emitted
  -> recipient reads body
  -> message-read event emitted
  -> recipient acks
  -> message-acked event emitted
  -> optional replies link into the thread
```

Pane closure does not delete messages immediately. Deliveries for a closed pane
become orphaned and are retained until normal retention pruning. New sends to a
closed pane fail.

### Event Types

Add mailbox event types to the existing NDJSON event stream:

- `message-delivered`
- `message-read`
- `message-acked`
- `message-replied`
- `message-expired`
- `message-pruned`
- `message-orphaned`

Event payloads carry only summaries:

```json
{
  "type": "message-delivered",
  "ts": "2026-05-30T12:00:00.000000000Z",
  "generation": 42,
  "pane_id": 2,
  "pane_name": "pane-2",
  "message": {
    "id": "msg-000123",
    "from": {"id": 1, "name": "pane-1", "host": "local"},
    "subject": "Review ready",
    "topics": ["review"],
    "groups": ["backend"],
    "thread_id": "msg-000123",
    "body_size": 27,
    "part_count": 1,
    "created_at": "2026-05-30T12:00:00.000000000Z"
  }
}
```

This requires extending the event payload shape with an optional mailbox summary
field. It should remain additive so existing event consumers ignore it.

### Capture JSON

Capture JSON includes an unread mailbox summary per pane:

```json
{
  "id": 2,
  "name": "pane-2",
  "mailbox": {
    "unread": 3,
    "latest_unread": [
      {
        "id": "msg-000123",
        "from": {"id": 1, "name": "pane-1", "host": "local"},
        "subject": "Review ready",
        "topics": ["review"],
        "thread_id": "msg-000123",
        "created_at": "2026-05-30T12:00:00.000000000Z",
        "body_size": 27,
        "part_count": 1
      }
    ],
    "topics": {"review": 2, "status": 1}
  }
}
```

Constraints:

- No bodies.
- No arbitrary metadata values.
- No read, ack, or reply state beyond what is needed to identify unread
  messages.
- Limit `latest_unread` to a small fixed count, for example 5.
- Include counts even when the pane content is omitted or capture is
  full-session.
- Preserve additive JSON behavior: consumers must ignore unknown fields.

### Protocol

The CLI can use ordinary `MsgTypeCommand` requests for all synchronous
operations. No high-volume binary frame is needed for message bodies because the
mailbox limits are intentionally small.

If interactive clients need live badge updates without polling, layout snapshots
can carry per-pane unread counts by adding summary fields to `PaneSnapshot`.
This is better than inventing a client-only mailbox protocol in v1 because the
server remains the source of truth and existing layout broadcasts already
describe pane metadata for rendering.

## Rendering

Rendering is optional and summary-only.

Recommended v1 rendering is a compact status-line badge:

```text
[ pane-2 msg:3 main ~/amux ]
```

Constraints:

- Show only unread count, never subject, sender, metadata, or body.
- Cap display at a short value such as `msg:9+`.
- Hide the badge before truncating pane identity in narrow cells.
- Do not consume pane content area or create overlays automatically.
- Do not animate or flash on delivery.
- Use existing status-line color plumbing and config palette constants; do not
  hardcode hex colors.

A global session-bar aggregate, for example `msgs:7`, can be a later addition
if users need cross-pane visibility. It should follow the same summary-only
rules.

## Persistence And Checkpointing

The mailbox is in-memory session state, but it must survive normal amux
preservation paths:

### Hot Reload

Hot-reload checkpoints should include the full mailbox: messages, delivery
state, thread links, sequence counter, and retention metadata. This keeps
mailbox behavior aligned with pane history and pane metadata during binary
reload.

### Crash Recovery

Crash checkpoints should include the mailbox by default, bounded by the same
size limits. The checkpoint file already persists pane history and screen
content, so persisting mailbox bodies is not a new storage class, but the
security documentation should call it out explicitly. Files must keep the
existing private state-directory permissions.

If mailbox persistence becomes configurable later, the setting must be explicit
and visible in docs because disabling it weakens delivery guarantees.

### Compatibility

Checkpoint additions should be additive:

- old checkpoints restore with an empty mailbox
- new checkpoints restore message IDs and delivery state exactly
- unknown future metadata fields are ignored or preserved depending on the
  serialized representation

### Retention

Retention is server-owned and policy-light:

- unread messages are retained until read, acked, expired, or forced out by a
  hard cap
- fully acked messages can be pruned after a short retention window
- orphaned messages for closed panes can be pruned after a separate window
- pruning emits summary-only `message-pruned` events

No v1 user-facing `msg prune` command is required.

## Failure Behavior

No-op failures must be loud:

| Case | Behavior |
| --- | --- |
| unknown sender or recipient | non-zero error, no message stored |
| ambiguous pane prefix | non-zero error, no message stored |
| no recipients after filtering | non-zero error |
| empty body | non-zero error unless `--allow-empty` is explicitly added later |
| oversize body or metadata | non-zero error with the relevant limit |
| invalid JSON metadata | non-zero error |
| invalid topic/group name | non-zero error |
| read by non-recipient pane | non-zero error |
| ack by non-recipient pane | non-zero error |
| wait target pane exits | non-zero error |
| wait timeout | non-zero timeout error |

Empty inbox results are not failures. Repeating the same ack is idempotent but
must report the existing ack state so callers can tell no state changed.

## Testing

Implementation beads should cover:

- unit tests for message ID allocation, recipient resolution, validation, state
  transitions, thread/reply links, retention, and checkpoint round trips
- direct command-handler tests for `send`, `inbox`, `read`, and `ack`
- integration tests with the server harness for `msg watch` and `wait msg`
  initial-state/race behavior
- capture JSON tests proving summaries appear and bodies/metadata values do not
- rendering golden tests only if the unread badge ships
- subprocess CLI tests through the shared hermetic helper, not open-coded
  `exec.Command`

Per repo practice, any newly added or modified targeted test slice should pass
with `-count=100` before the implementation bead is called done.

## Non-Goals

- No orca task semantics.
- No A2A bridge.
- No Linear, GitHub PR, or review policy.
- No Codex, Claude Code, Aider, Gemini, or other agent-specific behavior.
- No PTY prompt injection.
- No cross-session or cross-host delivery.
- No mailbox ACL model beyond the existing same-user amux socket boundary.
- No guaranteed exactly-once processing by external tools. amux records
  delivery/read/ack state; consumers decide how to act on it.
- No notification overlay that opens automatically on delivery.

## Recommended Bead Breakdown

If the existing beads need refinement, split implementation in this order:

1. **Mailbox model and checkpoint format.** Add the in-memory store, validation,
   ID allocation, thread links, delivery state, retention hooks, and hot/crash
   checkpoint serialization. No CLI beyond tests.
2. **Core CLI: `msg send`, `inbox`, `read`, `ack`.** Wire command handlers to
   the model, including JSON output and loud error behavior.
3. **Events and waits: `msg watch`, `wait msg`.** Add lifecycle events, initial
   snapshot behavior, filters, timeouts, and race-free wait semantics.
4. **Capture summaries.** Add per-pane unread summaries to capture JSON and
   prove bodies and metadata values never appear there.
5. **Optional rendering badge.** Add the status-line unread count with narrow
   cell constraints and golden coverage.
6. **Polish and docs.** Update README CLI reference, limits/security docs, and
   any config docs if retention or persistence knobs are exposed.

The first two beads make the mailbox usable. The third bead makes it reactive.
Capture and rendering should come after the state machine is stable so they
remain summary projections, not alternate sources of truth.

## Decision Log

| Date | Decision | Why |
| --- | --- | --- |
| 2026-05-30 | Mailbox is amux-owned, session-scoped, and server-owned | Keeps the primitive useful to any client while preserving amux's client-server architecture. |
| 2026-05-30 | Delivery never writes to PTYs | Prevents prompt corruption and keeps mailbox behavior distinct from `send-keys`. |
| 2026-05-30 | Full bodies are only available via `msg read` | Capture, events, and rendering are for awareness; reading is an explicit act. |
| 2026-05-30 | Topics/groups are labels in v1, not policy | Supports filtering without baking in orchestration semantics or requiring group-management commands. |
| 2026-05-30 | Read and ack are separate recipient states | A pane can inspect a message without committing to action, and can ack without amux interpreting the meaning. |
| 2026-05-30 | Checkpoint the mailbox with bounded bodies | Preserves delivery guarantees across reload/crash while keeping disk exposure explicit and capped. |
