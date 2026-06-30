package transport

import (
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pradeep-av/agent-swarm/internal/protocol"
	"github.com/pradeep-av/agent-swarm/internal/registry"
	"github.com/pradeep-av/agent-swarm/internal/scheduler"
)

const (
	writeWait      = 10 * time.Second
	pongWait       = 60 * time.Second
	pingPeriod     = (pongWait * 9) / 10
	maxMessageSize = 512 * 1024 // 512 KB
	sendBufSize    = 256
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	// Production deployments should validate the origin header.
	CheckOrigin: func(r *http.Request) bool { return true },
}

// Hub handles all incoming WebSocket connections from swarm-agents.
type Hub struct {
	registry  *registry.Registry
	scheduler *scheduler.Scheduler
	token     string // pre-shared auth token; empty means no auth
}

// NewHub constructs a Hub. token is the pre-shared key agents must present via
// "Authorization: Bearer <token>"; pass an empty string to disable auth.
func NewHub(reg *registry.Registry, sched *scheduler.Scheduler, token string) *Hub {
	return &Hub{registry: reg, scheduler: sched, token: token}
}

// ServeHTTP upgrades incoming HTTP requests to WebSocket and starts agent I/O pumps.
func (h *Hub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.token != "" {
		auth := r.Header.Get("Authorization")
		if auth != "Bearer "+h.token {
			log.Printf("transport: rejected connection from %s: bad or missing token", r.RemoteAddr)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("transport: websocket upgrade error: %v", err)
		return
	}

	client := &agentConn{
		hub:  h,
		conn: conn,
		send: make(chan []byte, sendBufSize),
	}

	go client.writePump()
	client.readPump() // blocks until disconnected
}

// agentConn represents one connected swarm-agent.
type agentConn struct {
	hub      *Hub
	conn     *websocket.Conn
	send     chan []byte
	workerID string
}

func (c *agentConn) readPump() {
	defer func() {
		if c.workerID != "" {
			c.hub.registry.Unregister(c.workerID)
			log.Printf("transport: worker %s disconnected", c.workerID)
		}
		close(c.send)
		c.conn.Close()
	}()

	c.conn.SetReadLimit(maxMessageSize)
	c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error {
		c.conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	for {
		_, data, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("transport: read error from worker %s: %v", c.workerID, err)
			}
			return
		}
		c.handleMessage(data)
	}
}

func (c *agentConn) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		c.conn.Close()
	}()

	for {
		select {
		case msg, ok := <-c.send:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				// send channel was closed
				c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			if err := c.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				log.Printf("transport: write error to worker %s: %v", c.workerID, err)
				return
			}
		case <-ticker.C:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

func (c *agentConn) handleMessage(data []byte) {
	var msg protocol.Message
	if err := json.Unmarshal(data, &msg); err != nil {
		log.Printf("transport: invalid message from worker %s: %v", c.workerID, err)
		return
	}

	switch msg.Type {

	case protocol.TypeRegister:
		var p protocol.RegisterPayload
		if err := json.Unmarshal(msg.Payload, &p); err != nil {
			log.Printf("transport: invalid register payload: %v", err)
			return
		}
		c.workerID = p.ID
		c.hub.registry.Register(&registry.Worker{
			ID:           p.ID,
			Hostname:     p.Hostname,
			Models:       p.Models,
			Capabilities: p.Capabilities,
			Labels:       p.Labels,
			LastSeen:     time.Now(),
			Send:         c.send,
		})
		log.Printf("transport: worker registered id=%s hostname=%s caps=%v", p.ID, p.Hostname, p.Capabilities)

	case protocol.TypeHeartbeat:
		var p protocol.HeartbeatPayload
		if err := json.Unmarshal(msg.Payload, &p); err != nil {
			return
		}
		c.hub.registry.UpdateHeartbeat(p.WorkerID)

	case protocol.TypeProgress:
		var p protocol.ProgressPayload
		if err := json.Unmarshal(msg.Payload, &p); err != nil {
			return
		}
		log.Printf("transport: [job %s] %s", p.JobID, p.Content)

	case protocol.TypeCompleted:
		var p protocol.CompletedPayload
		if err := json.Unmarshal(msg.Payload, &p); err != nil {
			return
		}
		c.hub.scheduler.Complete(p.JobID, p.Response, p.ExitCode)

	case protocol.TypeError:
		var p protocol.ErrorPayload
		if err := json.Unmarshal(msg.Payload, &p); err != nil {
			return
		}
		c.hub.scheduler.Fail(p.JobID, p.Message)

	default:
		log.Printf("transport: unknown message type %q from worker %s", msg.Type, c.workerID)
	}
}
