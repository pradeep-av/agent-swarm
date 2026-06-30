# agent-swarm

A generic agent swarm MCP server that lets a single OpenCode instance transparently delegate work to multiple remote OpenCode instances running premium models.

The orchestrator (`swarmd`) performs planning and routing only. All reasoning and execution happens on remote workers (`swarm-agent`).

---

## Architecture

```
User
  │
OpenCode  (planner model)
  │
MCP tools: delegate_task, continue_task, task_status
  │
swarmd  (any server)
  │
Persistent WebSocket connections
  ├── swarm-agent  (Laptop    → OpenCode + premium model)
  ├── swarm-agent  (Lab VM    → OpenCode + premium model)
  └── swarm-agent  (Desktop   → OpenCode + premium model)
```

---

## Prerequisites

- Go 1.22+
- `opencode` CLI installed and on `$PATH` on every worker machine

---

## Build

```sh
make build          # produces bin/swarmd and bin/swarm-agent
make vet            # go vet ./...
make test           # go test ./...
make clean          # remove bin/
```

---

## Running swarmd

`swarmd` runs on your central server. It speaks MCP over **stdio** (consumed by OpenCode) and listens for `swarm-agent` WebSocket connections on a separate HTTP port.

```sh
./bin/swarmd -addr :8080
```

| Flag | Default | Description |
|------|---------|-------------|
| `-addr` | `:8080` | Listen address for swarm-agent WebSocket connections |

### Wiring into OpenCode

Add `swarmd` as an MCP server in your OpenCode config:

```json
{
  "mcpServers": {
    "agent-swarm": {
      "command": "/path/to/bin/swarmd",
      "args": ["-addr", ":8080"]
    }
  }
}
```

OpenCode will then have access to the swarm tools (`delegate_task`, `continue_task`, `task_status`).

---

## Running swarm-agent

Run one `swarm-agent` on every machine that has OpenCode installed.

```sh
./bin/swarm-agent \
  -swarmd ws://<swarmd-host>:8080/agents \
  -capabilities coding,git \
  -models "GPT-5.5" \
  -opencode /usr/local/bin/opencode
```

| Flag | Default | Description |
|------|---------|-------------|
| `-swarmd` | `ws://localhost:8080/agents` | swarmd WebSocket URL |
| `-id` | `<hostname>-<random>` | Stable worker ID |
| `-hostname` | `os.Hostname()` | Display hostname |
| `-capabilities` | `coding` | Comma-separated capability labels used for routing |
| `-models` | _(empty)_ | Comma-separated model names (informational) |
| `-labels` | _(empty)_ | Arbitrary comma-separated labels |
| `-opencode` | `opencode` | Path to the opencode binary (default executor) |
| `-exec-bin` | _(empty)_ | Custom executor binary — overrides `-opencode` when set |
| `-exec-args` | _(empty)_ | Comma-separated arg template for the custom executor (required when `-exec-bin` is set) |

### Executor

By default the agent executes prompts via the OpenCode CLI:

```
opencode run --session {session} --print-response -- {prompt}
```

You can replace this with any CLI tool using `-exec-bin` and `-exec-args`.  
Two tokens are substituted at runtime:

| Token | Replaced with |
|-------|---------------|
| `{session}` | Remote session ID |
| `{prompt}` | Prompt text |

**Example — use a hypothetical `aicli` tool:**

```sh
./bin/swarm-agent \
  -swarmd ws://<swarmd-host>:8080/agents \
  -exec-bin aicli \
  -exec-args "chat,--session-id,{session},--message,{prompt}"
```

**Example — use Claude via a wrapper script:**

```sh
./bin/swarm-agent \
  -swarmd ws://<swarmd-host>:8080/agents \
  -exec-bin /usr/local/bin/claude-runner \
  -exec-args "--session,{session},--prompt,{prompt},--output-format,text"
```

The agent reconnects automatically if the connection to `swarmd` drops.

### Example: three workers

**Laptop** (coding tasks):
```sh
./bin/swarm-agent -swarmd ws://<swarmd-host>:8080/agents \
  -capabilities coding,git -models "GPT-5.5"
```

**Lab VM** (Kubernetes tasks):
```sh
./bin/swarm-agent -swarmd ws://<swarmd-host>:8080/agents \
  -capabilities kubernetes,coding -models "Claude-4"
```

**Desktop** (documentation):
```sh
./bin/swarm-agent -swarmd ws://<swarmd-host>:8080/agents \
  -capabilities docs,coding -models "Gemini-2.5"
```

---

## Using swarm tools from OpenCode

The swarm exposes these MCP tools:

```
delegate_task(session_id, prompt)
continue_task(session_id, prompt)
task_status(session_id)
```

### delegate_task

Use this to start or route a task.

| Parameter | Description |
|-----------|-------------|
| `session_id` | Unique string identifying the conversation. Reuse the same ID to keep continuity. |
| `prompt` | The task or question to route to a remote OpenCode instance. |

Example:

```
Use delegate_task with session_id="proj-auth" and prompt="Fix the JWT expiry bug in auth/token.go"
```

### continue_task

Use this for follow-up prompts on an existing task session.

| Parameter | Description |
|-----------|-------------|
| `session_id` | Existing task session to continue. |
| `prompt` | Follow-up prompt for that session. |

Example:

```
Use continue_task with session_id="proj-auth" and prompt="Add regression tests for the token expiry path"
```

### task_status

Use this to inspect current routing and worker state for a session.

| Parameter | Description |
|-----------|-------------|
| `session_id` | Session to inspect. |

Example:

```
Use task_status with session_id="proj-auth"
```

`task_status` returns JSON with fields like `found`, `worker_id`, `remote_session`, `worker_busy`, and `last_access`.

### Compatibility

`swarm_chat(session_id, prompt)` is still available as a backward-compatible alias for `delegate_task`.

The planner routes the prompt to the best available worker based on keyword rules:

| Keywords in prompt | Routed to workers with capability |
|--------------------|-----------------------------------|
| `kubernetes`, `k8s`, `helm`, `pod` | `kubernetes` |
| `code`, `fix`, `refactor`, `debug`, `test` | `coding` |
| `doc`, `documentation`, `readme` | `docs` |

If no capability matches, the first free worker is used.

---

## Session continuity

The first call with a given `session_id` picks a worker. Every subsequent call with the same `session_id` is forwarded to the **same worker and the same remote OpenCode session**, preserving full conversational context.

---

## Project layout

```
cmd/
  swarmd/           orchestrator binary
  swarm-agent/      worker agent binary

internal/
  mcp/              MCP server (delegate_task, continue_task, task_status)
  planner/          rule-based worker selector
  scheduler/        job dispatch and result tracking
  registry/         in-memory worker registry
  session/          master→worker→remote-session mapping
  transport/        WebSocket hub (per-agent read/write pumps)
  protocol/         shared message types

pkg/
  opencode/         OpenCode CLI executor interface
```
