package main

import (
	"flag"
	"log"
	"net/http"

	"github.com/mark3labs/mcp-go/server"
	"github.com/pradeep-av/agent-swarm/internal/mcp"
	"github.com/pradeep-av/agent-swarm/internal/registry"
	"github.com/pradeep-av/agent-swarm/internal/scheduler"
	"github.com/pradeep-av/agent-swarm/internal/session"
	"github.com/pradeep-av/agent-swarm/internal/transport"
)

func main() {
	addr := flag.String("addr", ":8080", "listen address for swarm-agent WebSocket connections and status UI")
	token := flag.String("token", "", "pre-shared auth token; agents must present this via Authorization: Bearer. Empty = no auth")
	flag.Parse()

	if *token == "" {
		log.Printf("swarmd: WARNING — no -token set, agent endpoint is unauthenticated")
	}

	reg := registry.New()
	sess := session.NewStore()
	sched := scheduler.New(reg, sess)

	hub := transport.NewHub(reg, sched, *token)
	status := transport.NewStatusHandler(reg, *token)
	mcpServer := mcp.NewServer(sched, reg)

	mux := http.NewServeMux()
	mux.Handle("/agents", hub)
	mux.Handle("/status", status)
	mux.Handle("/", status)

	go func() {
		log.Printf("swarmd: listening on %s  (agents=/agents  ui=/)", *addr)
		if err := http.ListenAndServe(*addr, mux); err != nil {
			log.Fatalf("swarmd: HTTP server error: %v", err)
		}
	}()

	log.Printf("swarmd: starting MCP server on stdio")
	if err := server.ServeStdio(mcpServer); err != nil {
		log.Fatalf("swarmd: MCP server error: %v", err)
	}
}
