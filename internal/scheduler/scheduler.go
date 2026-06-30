package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/pradeep-av/agent-swarm/internal/protocol"
	"github.com/pradeep-av/agent-swarm/internal/registry"
	"github.com/pradeep-av/agent-swarm/internal/session"
)

const (
	sendTimeout = 5 * time.Second
	jobTimeout  = 10 * time.Minute
)

// JobResult carries the outcome of a dispatched job.
type JobResult struct {
	Response string
	ExitCode int
	Err      error
}

// SessionStatus describes the known routing state for a master session.
type SessionStatus struct {
	Found         bool
	MasterSession string
	WorkerID      string
	WorkerBusy    bool
	LastAccess    time.Time
}

type pendingJob struct {
	jobID    string
	workerID string
	resultCh chan JobResult
}

// Scheduler routes prompts to workers and tracks in-flight jobs.
type Scheduler struct {
	mu          sync.Mutex
	registry    *registry.Registry
	sessions    *session.Store
	pendingJobs map[string]*pendingJob
}

// New returns a ready Scheduler.
func New(reg *registry.Registry, sess *session.Store) *Scheduler {
	return &Scheduler{
		registry:    reg,
		sessions:    sess,
		pendingJobs: make(map[string]*pendingJob),
	}
}

// Dispatch routes a prompt to the correct worker and blocks until the result arrives.
// target is optional: an exact worker ID or a capability name. Empty = any free worker.
// For sessions that already exist the target is ignored — the same worker is always reused.
func (s *Scheduler) Dispatch(ctx context.Context, masterSessionID, prompt, target string) (string, error) {
	var workerID string

	if sess, ok := s.sessions.Get(masterSessionID); ok {
		// Existing session: always route to the same worker for continuity.
		workerID = sess.WorkerID
		s.sessions.Touch(masterSessionID)
	} else {
		// New session: select worker based on target.
		worker := s.selectWorker(target)
		if worker == nil {
			if target != "" {
				return "", fmt.Errorf("no available worker matching %q", target)
			}
			return "", fmt.Errorf("no available workers")
		}
		workerID = worker.ID
		s.sessions.Save(&session.Session{
			MasterID: masterSessionID,
			WorkerID: workerID,
		})
	}

	worker, ok := s.registry.Get(workerID)
	if !ok {
		return "", fmt.Errorf("worker %s not found in registry", workerID)
	}

	jobID := uuid.New().String()

	payloadBytes, err := json.Marshal(protocol.JobPayload{
		JobID:  jobID,
		Prompt: prompt,
	})
	if err != nil {
		return "", fmt.Errorf("marshal job payload: %w", err)
	}

	msgBytes, err := json.Marshal(protocol.Message{
		Type:    protocol.TypeJob,
		Payload: payloadBytes,
	})
	if err != nil {
		return "", fmt.Errorf("marshal job message: %w", err)
	}

	pending := &pendingJob{
		jobID:    jobID,
		workerID: workerID,
		resultCh: make(chan JobResult, 1),
	}

	s.mu.Lock()
	s.pendingJobs[jobID] = pending
	s.mu.Unlock()

	s.registry.SetBusy(workerID, true, masterSessionID)

	select {
	case worker.Send <- msgBytes:
	case <-ctx.Done():
		s.cleanup(jobID, workerID)
		return "", ctx.Err()
	case <-time.After(sendTimeout):
		s.cleanup(jobID, workerID)
		return "", fmt.Errorf("timeout sending job to worker %s", workerID)
	}

	select {
	case result := <-pending.resultCh:
		s.cleanup(jobID, workerID)
		if result.Err != nil {
			return "", result.Err
		}
		return result.Response, nil
	case <-ctx.Done():
		s.cleanup(jobID, workerID)
		return "", ctx.Err()
	case <-time.After(jobTimeout):
		s.cleanup(jobID, workerID)
		return "", fmt.Errorf("job %s timed out after %v", jobID, jobTimeout)
	}
}

// Complete resolves a pending job with a successful result.
func (s *Scheduler) Complete(jobID, response string, exitCode int) {
	s.mu.Lock()
	pending, ok := s.pendingJobs[jobID]
	s.mu.Unlock()
	if !ok {
		return
	}
	pending.resultCh <- JobResult{Response: response, ExitCode: exitCode}
}

// Fail resolves a pending job with an error.
func (s *Scheduler) Fail(jobID, errMsg string) {
	s.mu.Lock()
	pending, ok := s.pendingJobs[jobID]
	s.mu.Unlock()
	if !ok {
		return
	}
	pending.resultCh <- JobResult{Err: fmt.Errorf("%s", errMsg)}
}

func (s *Scheduler) cleanup(jobID, workerID string) {
	s.mu.Lock()
	delete(s.pendingJobs, jobID)
	s.mu.Unlock()
	s.registry.SetBusy(workerID, false, "")
}

// Status returns the current routing state for a master session.
func (s *Scheduler) Status(masterSessionID string) SessionStatus {
	sess, ok := s.sessions.Get(masterSessionID)
	if !ok {
		return SessionStatus{Found: false, MasterSession: masterSessionID}
	}

	status := SessionStatus{
		Found:         true,
		MasterSession: masterSessionID,
		WorkerID:      sess.WorkerID,
		LastAccess:    sess.LastAccess,
	}

	if worker, exists := s.registry.Get(sess.WorkerID); exists {
		status.WorkerBusy = worker.Busy
	}

	return status
}

// selectWorker finds a free worker matching target (worker ID or capability).
// If target is empty, returns any free worker. Returns nil if none found.
func (s *Scheduler) selectWorker(target string) *registry.Worker {
	workers := s.registry.All()
	if target == "" {
		for _, w := range workers {
			if !w.Busy {
				return w
			}
		}
		return nil
	}
	// Exact worker ID match first.
	for _, w := range workers {
		if !w.Busy && w.ID == target {
			return w
		}
	}
	// Capability match (case-insensitive).
	lower := strings.ToLower(target)
	for _, w := range workers {
		if w.Busy {
			continue
		}
		for _, cap := range w.Capabilities {
			if strings.ToLower(cap) == lower {
				return w
			}
		}
	}
	return nil
}
