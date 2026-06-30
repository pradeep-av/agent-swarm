package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	mcpgo "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/pradeep-av/agent-swarm/internal/registry"
	"github.com/pradeep-av/agent-swarm/internal/scheduler"
)

// NewServer builds and returns the MCP server and its tools.
func NewServer(sched *scheduler.Scheduler, reg *registry.Registry) *server.MCPServer {
	s := server.NewMCPServer(
		"agent-swarm",
		"0.1.0",
		server.WithToolCapabilities(false),
	)

	delegateTool := mcpgo.NewTool("delegate_task",
		mcpgo.WithDescription(
			"Delegate a task to the agent swarm and wait for the response. "+
				"Blocks until the worker finishes and returns the full response — no polling needed. "+
				"Call list_agents first to see available workers, then specify a target worker ID or "+
				"capability to route to. Omit target to use any free worker.",
		),
		mcpgo.WithString("session_id",
			mcpgo.Required(),
			mcpgo.Description("Unique identifier for the conversation session."),
		),
		mcpgo.WithString("prompt",
			mcpgo.Required(),
			mcpgo.Description("The task or question to send to the remote OpenCode instance."),
		),
		mcpgo.WithString("target",
			mcpgo.Description("Optional: exact worker ID or a capability name (e.g. \"coding\", \"kubernetes\"). Omit to use any free worker."),
		),
	)

	continueTool := mcpgo.NewTool("continue_task",
		mcpgo.WithDescription(
			"Continue an existing delegated task using the same session_id. "+
				"Blocks until the worker finishes and returns the full response. "+
				"Always routes to the original worker to preserve conversational context.",
		),
		mcpgo.WithString("session_id",
			mcpgo.Required(),
			mcpgo.Description("Existing master session ID to continue."),
		),
		mcpgo.WithString("prompt",
			mcpgo.Required(),
			mcpgo.Description("Follow-up prompt for the existing delegated task."),
		),
	)

	taskStatusTool := mcpgo.NewTool("task_status",
		mcpgo.WithDescription(
			"Get current routing and worker status for a session_id.",
		),
		mcpgo.WithString("session_id",
			mcpgo.Required(),
			mcpgo.Description("Master session ID to inspect."),
		),
	)

	listAgentsTool := mcpgo.NewTool("list_agents",
		mcpgo.WithDescription(
			"List all swarm-agents currently connected to swarmd, with their capabilities, "+
				"models, labels, busy/idle status, and when they were last seen.",
		),
	)

	legacyTool := mcpgo.NewTool("swarm_chat",
		mcpgo.WithDescription(
			"Compatibility alias for delegate_task. Prefer delegate_task for new clients.",
		),
		mcpgo.WithString("session_id",
			mcpgo.Required(),
			mcpgo.Description("Unique identifier for the conversation session."),
		),
		mcpgo.WithString("prompt",
			mcpgo.Required(),
			mcpgo.Description("The task or question to send to the remote OpenCode instance."),
		),
	)

	dispatchHandler := func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		args := req.GetArguments()
		sessionID, _ := args["session_id"].(string)
		prompt, _ := args["prompt"].(string)
		target, _ := args["target"].(string)

		if sessionID == "" {
			return mcpgo.NewToolResultError("session_id is required"), nil
		}
		if prompt == "" {
			return mcpgo.NewToolResultError("prompt is required"), nil
		}

		response, err := sched.Dispatch(ctx, sessionID, prompt, target)
		if err != nil {
			return mcpgo.NewToolResultError(fmt.Sprintf("dispatch failed: %v", err)), nil
		}

		return mcpgo.NewToolResultText(response), nil
	}

	statusHandler := func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		_ = ctx
		args := req.GetArguments()
		sessionID, _ := args["session_id"].(string)
		if sessionID == "" {
			return mcpgo.NewToolResultError("session_id is required"), nil
		}

		status := sched.Status(sessionID)
		payload, err := json.MarshalIndent(status, "", "  ")
		if err != nil {
			return mcpgo.NewToolResultError(fmt.Sprintf("status encode failed: %v", err)), nil
		}

		return mcpgo.NewToolResultText(string(payload)), nil
	}

	// agentView is the JSON shape exposed through the MCP tool.
	type agentView struct {
		ID             string    `json:"id"`
		Hostname       string    `json:"hostname"`
		Capabilities   []string  `json:"capabilities"`
		Models         []string  `json:"models"`
		Labels         []string  `json:"labels"`
		Busy           bool      `json:"busy"`
		CurrentSession string    `json:"current_session,omitempty"`
		LastSeen       time.Time `json:"last_seen"`
		SecondsSeen    int       `json:"seconds_since_seen"`
	}

	listAgentsHandler := func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		_ = ctx
		workers := reg.All()
		sort.Slice(workers, func(i, j int) bool { return workers[i].ID < workers[j].ID })

		views := make([]agentView, 0, len(workers))
		for _, w := range workers {
			views = append(views, agentView{
				ID:             w.ID,
				Hostname:       w.Hostname,
				Capabilities:   w.Capabilities,
				Models:         w.Models,
				Labels:         w.Labels,
				Busy:           w.Busy,
				CurrentSession: w.CurrentSession,
				LastSeen:       w.LastSeen,
				SecondsSeen:    int(time.Since(w.LastSeen).Seconds()),
			})
		}

		result := struct {
			Total  int         `json:"total"`
			Busy   int         `json:"busy"`
			Idle   int         `json:"idle"`
			Agents []agentView `json:"agents"`
		}{Total: len(views), Agents: views}
		for _, v := range views {
			if v.Busy {
				result.Busy++
			} else {
				result.Idle++
			}
		}

		payload, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			return mcpgo.NewToolResultError(fmt.Sprintf("encode failed: %v", err)), nil
		}
		return mcpgo.NewToolResultText(string(payload)), nil
	}

	s.AddTool(delegateTool, dispatchHandler)
	s.AddTool(continueTool, dispatchHandler)
	s.AddTool(legacyTool, dispatchHandler)
	s.AddTool(taskStatusTool, statusHandler)
	s.AddTool(listAgentsTool, listAgentsHandler)

	return s
}
