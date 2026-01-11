package varnishadm

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"sync"
)

// Server listens for connections from varnishd instances started with -M.
type Server struct {
	listener   net.Listener
	secret     []byte
	callbacks  *Callbacks
	logger     *slog.Logger
	conns      map[*Conn]struct{}
	mu         sync.RWMutex
	cmdTimeout int // command timeout in seconds (0 = use default)
}

// ServerOption configures a Server.
type ServerOption func(*Server)

// WithServerCallbacks sets the callbacks for connection events.
func WithServerCallbacks(cb *Callbacks) ServerOption {
	return func(s *Server) {
		s.callbacks = cb
	}
}

// WithLogger sets a logger for the server.
func WithLogger(logger *slog.Logger) ServerOption {
	return func(s *Server) {
		s.logger = logger
	}
}

// NewServer creates a new Server that listens for varnishd connections.
// listener is an already-listening net.Listener.
// secret is the shared secret for authentication.
func NewServer(listener net.Listener, secret string, opts ...ServerOption) *Server {
	s := &Server{
		listener: listener,
		secret:   []byte(secret),
		conns:    make(map[*Conn]struct{}),
		logger:   slog.Default(),
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// NewServerFromSecretFile creates a Server reading the secret from a file.
func NewServerFromSecretFile(listener net.Listener, secretPath string, opts ...ServerOption) (*Server, error) {
	secret, err := ReadSecretFile(secretPath)
	if err != nil {
		return nil, err
	}
	return NewServer(listener, string(secret), opts...), nil
}

// Accept accepts a single connection from varnishd.
// This blocks until a connection is made or an error occurs.
// For context cancellation support, use Run() or close the listener externally.
// Returns the authenticated Conn ready for use.
func (s *Server) Accept(ctx context.Context) (*Conn, error) {
	// Check if context is already cancelled
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	netConn, err := s.listener.Accept()
	if err != nil {
		// Check if this was due to context cancellation
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
			return nil, fmt.Errorf("accept: %w", err)
		}
	}

	remoteAddr := ""
	if netConn.RemoteAddr() != nil {
		remoteAddr = netConn.RemoteAddr().String()
	}

	s.logger.Debug("connection from varnishd", "remote_addr", remoteAddr)

	// Authenticate the connection
	auth, err := Authenticate(netConn, s.secret)
	if err != nil {
		netConn.Close()
		s.callbacks.invokeAuthFail(remoteAddr, err)
		return nil, fmt.Errorf("authenticate: %w", err)
	}

	// Create Conn with server-mode options
	var connOpts []ConnOption
	if s.callbacks != nil {
		connOpts = append(connOpts, WithConnCallbacks(s.callbacks))
	}

	conn := newConn(netConn, ModeServer, auth, connOpts...)

	// Track connection
	s.mu.Lock()
	s.conns[conn] = struct{}{}
	s.mu.Unlock()

	s.logger.Info("varnishd connected and authenticated",
		"version", conn.Version(),
		"remote_addr", remoteAddr)

	s.callbacks.invokeConnect(conn)

	return conn, nil
}

// Run accepts connections in a loop, calling OnConnect for each.
// It blocks until the context is cancelled.
// Each accepted connection is passed to the OnConnect callback.
// The callback is responsible for handling the connection (e.g., in a goroutine).
func (s *Server) Run(ctx context.Context) error {
	s.logger.Debug("starting server", "addr", s.listener.Addr())

	// Close listener when context is done
	go func() {
		<-ctx.Done()
		s.listener.Close()
	}()

	for {
		conn, err := s.Accept(ctx)
		if err != nil {
			select {
			case <-ctx.Done():
				s.logger.Debug("context cancelled, stopping server")
				return nil
			default:
				s.logger.Error("accept failed", "error", err)
				continue
			}
		}

		// Callback handles what to do with the connection
		// (e.g., store it, start a handler goroutine)
		if s.callbacks != nil && s.callbacks.OnConnect != nil {
			// OnConnect was already called in Accept, so we just continue
			_ = conn // connection is tracked and callback was called
		}
	}
}

// Shutdown gracefully closes all active connections and the listener.
func (s *Server) Shutdown(ctx context.Context) error {
	s.logger.Debug("shutting down server")

	// Close the listener first
	if err := s.listener.Close(); err != nil {
		s.logger.Error("close listener failed", "error", err)
	}

	// Close all active connections
	s.mu.Lock()
	for conn := range s.conns {
		if err := conn.Close(); err != nil {
			s.logger.Error("close connection failed", "error", err)
		}
	}
	s.conns = make(map[*Conn]struct{})
	s.mu.Unlock()

	return nil
}

// Connections returns the number of active connections.
func (s *Server) Connections() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.conns)
}

// removeConn removes a connection from tracking (called on close).
func (s *Server) removeConn(conn *Conn) {
	s.mu.Lock()
	delete(s.conns, conn)
	s.mu.Unlock()
}

