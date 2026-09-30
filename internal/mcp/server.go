// Package mcp stellt die Historie ueber einen MCP-Server bereit, damit ein
// beliebiges LLM sie abfragen kann.
package mcp

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// Server bedient die MCP-Tools. Er ist ausschliesslich lesend.
type Server struct {
	backend Backend
	cluster string
	now     func() time.Time
}

// Options konfiguriert den MCP-Server.
type Options struct {
	Backend Backend
	Cluster string
	// Now ist injizierbar, damit Tests relative Zeitangaben festnageln koennen.
	Now func() time.Time
}

// NewServer baut den Server.
func NewServer(opts Options) *Server {
	if opts.Now == nil {
		opts.Now = func() time.Time { return time.Now().UTC() }
	}
	return &Server{backend: opts.Backend, cluster: opts.Cluster, now: opts.Now}
}

// MCPServer liefert den fertig bestueckten SDK-Server. Tests sprechen ihn
// ueber einen In-Process-Transport an, ohne HTTP.
func (s *Server) MCPServer() *mcpsdk.Server {
	srv := mcpsdk.NewServer(&mcpsdk.Implementation{
		Name:    "stateinspector",
		Version: "0.1.0",
	}, nil)
	s.register(srv)
	return srv
}

// Listener bindet den MCP-Server an eine Adresse.
type Listener struct {
	server *Server
	addr   string
}

// NewListener baut den HTTP-Teil. Die Adresse ist per Default loopback; der
// Server hat keine Authentifizierung und darf deshalb nicht ins Netz (R20).
func NewListener(server *Server, addr string) *Listener {
	return &Listener{server: server, addr: addr}
}

// NeedLeaderElection ist false: Lesen darf jede Replik, auch ohne Lease.
func (l *Listener) NeedLeaderElection() bool { return false }

// Start bedient /mcp, bis der Context endet.
func (l *Listener) Start(ctx context.Context) error {
	logger := log.FromContext(ctx).WithName("mcp")

	srv := l.server.MCPServer()
	mux := http.NewServeMux()
	mux.Handle("/mcp", mcpsdk.NewStreamableHTTPHandler(
		func(*http.Request) *mcpsdk.Server { return srv }, nil))

	httpServer := &http.Server{
		Addr:              l.addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	listener, err := net.Listen("tcp", l.addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", l.addr, err)
	}
	logger.Info("MCP-Server bereit", "addr", listener.Addr().String(), "path", "/mcp")

	errs := make(chan error, 1)
	go func() {
		if err := httpServer.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errs <- err
			return
		}
		errs <- nil
	}()

	select {
	case err := <-errs:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shut down mcp server: %w", err)
		}
		return nil
	}
}
