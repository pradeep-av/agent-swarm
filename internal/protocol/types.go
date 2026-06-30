package protocol

import "encoding/json"

// MessageType identifies the kind of WebSocket message exchanged between swarmd and swarm-agent.
type MessageType string

const (
	TypeRegister  MessageType = "register"
	TypeHeartbeat MessageType = "heartbeat"
	TypeJob       MessageType = "job"
	TypeProgress  MessageType = "progress"
	TypeCompleted MessageType = "completed"
	TypeError     MessageType = "error"
)

// Message is the envelope for all WebSocket communication.
type Message struct {
	Type    MessageType     `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

// RegisterPayload is sent by swarm-agent on connect.
type RegisterPayload struct {
	ID           string   `json:"id"`
	Hostname     string   `json:"hostname"`
	Labels       []string `json:"labels"`
	Capabilities []string `json:"capabilities"`
	Models       []string `json:"models"`
}

// HeartbeatPayload is sent by swarm-agent every 5 seconds.
type HeartbeatPayload struct {
	WorkerID string `json:"worker_id"`
}

// JobPayload is sent by swarmd to a swarm-agent to execute a prompt.
type JobPayload struct {
	JobID  string `json:"job_id"`
	Prompt string `json:"prompt"`
}

// ProgressPayload is streamed by swarm-agent while a job is running.
type ProgressPayload struct {
	JobID   string `json:"job_id"`
	Content string `json:"content"`
}

// CompletedPayload is sent by swarm-agent when a job finishes successfully.
type CompletedPayload struct {
	JobID    string `json:"job_id"`
	Response string `json:"response"`
	ExitCode int    `json:"exit_code"`
}

// ErrorPayload is sent by swarm-agent when a job fails.
type ErrorPayload struct {
	JobID   string `json:"job_id"`
	Message string `json:"message"`
}
