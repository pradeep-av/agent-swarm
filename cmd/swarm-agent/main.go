package main

import (
	"context"
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/pradeep-av/agent-swarm/internal/protocol"
	"github.com/pradeep-av/agent-swarm/pkg/opencode"
)

func main() {
	swarmdURL := flag.String("swarmd", "ws://localhost:8080/agents", "swarmd WebSocket URL")
	workerID := flag.String("id", "", "worker ID (defaults to <hostname>-<random>)")
	hostname := flag.String("hostname", "", "hostname label (defaults to os.Hostname)")
	caps := flag.String("capabilities", "coding", "comma-separated capability labels")
	models := flag.String("models", "", "comma-separated model names")
	labels := flag.String("labels", "", "comma-separated arbitrary labels")
	token := flag.String("token", "", "pre-shared auth token for swarmd (required when swarmd enforces auth)")
	// Executor configuration
	opencodebin := flag.String("opencode", "opencode", "path to the opencode binary (default executor)")
	execBin := flag.String("exec-bin", "", "custom executor binary; when set, -exec-args must also be provided")
	execArgs := flag.String("exec-args", "", "comma-separated arg template for the custom executor, e.g.\n\t\"run,--dangerously-skip-permissions,{prompt}\"")
	flag.Parse()

	h, _ := os.Hostname()
	if *hostname != "" {
		h = *hostname
	}
	if *workerID == "" {
		*workerID = h + "-" + uuid.New().String()[:8]
	}

	var executor opencode.Executor
	if *execBin != "" {
		tmpl := opencode.ParseArgTemplate(*execArgs)
		if len(tmpl) == 0 {
			log.Fatal("swarm-agent: -exec-bin requires -exec-args to be set")
		}
		executor = &opencode.CLIExecutor{Binary: *execBin, ArgTemplate: tmpl}
		log.Printf("swarm-agent: using custom executor binary=%s args=%v", *execBin, tmpl)
	} else {
		executor = opencode.NewExecutor(*opencodebin)
		log.Printf("swarm-agent: using opencode executor binary=%s", *opencodebin)
	}

	agent := &agent{
		workerID:     *workerID,
		hostname:     h,
		capabilities: splitCSV(*caps),
		models:       splitCSV(*models),
		labels:       splitCSV(*labels),
		swarmdURL:    *swarmdURL,
		token:        *token,
		executor:     executor,
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	agent.run(ctx)
}

type agent struct {
	workerID     string
	hostname     string
	capabilities []string
	models       []string
	labels       []string
	swarmdURL    string
	token        string
	executor     opencode.Executor
}

// run connects to swarmd and reconnects automatically on failure.
func (a *agent) run(ctx context.Context) {
	attempt := 0
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		attempt++
		if err := a.connect(ctx); err != nil {
			log.Printf("swarm-agent: connection error (attempt %d): %v — retrying in 5s", attempt, err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
			}
		} else {
			// clean disconnect (ctx cancelled); reset counter for next run
			attempt = 0
		}
	}
}

func (a *agent) connect(ctx context.Context) error {
	headers := http.Header{}
	if a.token != "" {
		headers.Set("Authorization", "Bearer "+a.token)
	}
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, a.swarmdURL, headers)
	if err != nil {
		return err
	}
	defer conn.Close()

	log.Printf("swarm-agent: connected to %s as worker %s (capabilities=%v models=%v labels=%v)",
		a.swarmdURL, a.workerID, a.capabilities, a.models, a.labels)

	if err := a.send(conn, protocol.TypeRegister, protocol.RegisterPayload{
		ID:           a.workerID,
		Hostname:     a.hostname,
		Capabilities: a.capabilities,
		Models:       a.models,
		Labels:       a.labels,
	}); err != nil {
		return err
	}

	heartbeat := time.NewTicker(5 * time.Second)
	defer heartbeat.Stop()

	msgCh := make(chan []byte, 32)
	errCh := make(chan error, 1)

	go func() {
		for {
			_, data, err := conn.ReadMessage()
			if err != nil {
				errCh <- err
				return
			}
			msgCh <- data
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return nil

		case err := <-errCh:
			log.Printf("swarm-agent: disconnected from %s: %v", a.swarmdURL, err)
			return err

		case <-heartbeat.C:
			if err := a.send(conn, protocol.TypeHeartbeat, protocol.HeartbeatPayload{
				WorkerID: a.workerID,
			}); err != nil {
				return err
			}

		case data := <-msgCh:
			var msg protocol.Message
			if err := json.Unmarshal(data, &msg); err != nil {
				log.Printf("swarm-agent: invalid message: %v", err)
				continue
			}
			switch msg.Type {
			case protocol.TypeJob:
				var job protocol.JobPayload
				if err := json.Unmarshal(msg.Payload, &job); err != nil {
					log.Printf("swarm-agent: invalid job payload: %v", err)
					continue
				}
				go a.handleJob(ctx, conn, job)
			default:
				log.Printf("swarm-agent: unexpected message type %q — ignoring", msg.Type)
			}
		}
	}
}

func (a *agent) handleJob(ctx context.Context, conn *websocket.Conn, job protocol.JobPayload) {
	promptPreview := job.Prompt
	if len(promptPreview) > 120 {
		promptPreview = promptPreview[:120] + "..."
	}
	log.Printf("swarm-agent: received job=%s prompt=%q", job.JobID, promptPreview)

	start := time.Now()
	result, err := a.executor.Execute(ctx, job.Prompt)
	if err != nil {
		log.Printf("swarm-agent: job=%s failed after %s: %v", job.JobID, time.Since(start).Round(time.Millisecond), err)
		_ = a.send(conn, protocol.TypeError, protocol.ErrorPayload{
			JobID:   job.JobID,
			Message: err.Error(),
		})
		return
	}

	if result.ExitCode != 0 {
		log.Printf("swarm-agent: job=%s exited with code %d after %s; stderr=%q",
			job.JobID, result.ExitCode, time.Since(start).Round(time.Millisecond), result.Stderr)
	} else {
		log.Printf("swarm-agent: job=%s completed exit=0 response_len=%d duration=%s",
			job.JobID, len(result.Response), time.Since(start).Round(time.Millisecond))
	}

	_ = a.send(conn, protocol.TypeCompleted, protocol.CompletedPayload{
		JobID:    job.JobID,
		Response: result.Response,
		ExitCode: result.ExitCode,
	})
}

func (a *agent) send(conn *websocket.Conn, msgType protocol.MessageType, payload any) error {
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	data, err := json.Marshal(protocol.Message{Type: msgType, Payload: payloadBytes})
	if err != nil {
		return err
	}
	return conn.WriteMessage(websocket.TextMessage, data)
}

func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
