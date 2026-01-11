package varnishadm

import (
	"errors"
	"fmt"
	"net"
	"sync"
	"time"
)

// ConnectionMode indicates how the connection was established.
type ConnectionMode int

const (
	// ModeClient means we connected to a running varnishd instance.
	ModeClient ConnectionMode = iota
	// ModeServer means varnishd connected to us via -M flag.
	ModeServer
)

func (m ConnectionMode) String() string {
	switch m {
	case ModeClient:
		return "client"
	case ModeServer:
		return "server"
	default:
		return "unknown"
	}
}

// Conn represents a connection to a Varnish instance.
// It provides methods to execute commands and query state.
// Conn is safe for concurrent use.
type Conn struct {
	conn        net.Conn
	mode        ConnectionMode
	banner      string
	environment string
	version     string
	callbacks   *Callbacks
	mu          sync.Mutex
	closed      bool
	cmdTimeout  time.Duration
}

// ConnOption configures a Conn.
type ConnOption func(*Conn)

// WithConnCallbacks sets the callbacks for connection events.
func WithConnCallbacks(cb *Callbacks) ConnOption {
	return func(c *Conn) {
		c.callbacks = cb
	}
}

// WithCommandTimeout sets the timeout for command execution.
func WithCommandTimeout(d time.Duration) ConnOption {
	return func(c *Conn) {
		c.cmdTimeout = d
	}
}

// newConn creates a new Conn from an authenticated connection.
func newConn(conn net.Conn, mode ConnectionMode, auth *AuthInfo, opts ...ConnOption) *Conn {
	c := &Conn{
		conn:        conn,
		mode:        mode,
		banner:      auth.Banner,
		environment: auth.Environment,
		version:     auth.Version,
		cmdTimeout:  30 * time.Second, // default timeout
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// Mode returns the connection mode (client or server).
func (c *Conn) Mode() ConnectionMode {
	return c.mode
}

// Banner returns the full banner text received during authentication.
func (c *Conn) Banner() string {
	return c.banner
}

// Environment returns the parsed environment string (e.g., "Linux,6.8.0-79-generic,x86_64,...").
func (c *Conn) Environment() string {
	return c.environment
}

// Version returns the parsed Varnish version string.
func (c *Conn) Version() string {
	return c.version
}

// RemoteAddr returns the remote address of the connection.
func (c *Conn) RemoteAddr() net.Addr {
	return c.conn.RemoteAddr()
}

// LocalAddr returns the local address of the connection.
func (c *Conn) LocalAddr() net.Addr {
	return c.conn.LocalAddr()
}

// Close closes the connection.
func (c *Conn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return nil
	}
	c.closed = true

	err := c.conn.Close()
	c.callbacks.invokeDisconnect(c, err)
	return err
}

// IsClosed returns whether the connection has been closed.
func (c *Conn) IsClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

// Exec executes a command and returns the response.
// Returns an error if the command fails or times out.
func (c *Conn) Exec(cmd string) (VarnishResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return VarnishResponse{}, errors.New("connection closed")
	}

	if len(cmd) == 0 {
		return VarnishResponse{statusCode: ClisSyntax}, errors.New("empty command")
	}

	// Set deadline for write
	if err := WriteCommandWithDeadline(c.conn, cmd, c.cmdTimeout); err != nil {
		c.callbacks.invokeError(c, err)
		return VarnishResponse{statusCode: ClisComms}, fmt.Errorf("write command: %w", err)
	}

	// Read response
	status, body, err := ReadMessageWithDeadline(c.conn, c.cmdTimeout)
	if err != nil {
		c.callbacks.invokeError(c, err)
		return VarnishResponse{statusCode: ClisComms}, fmt.Errorf("read response: %w", err)
	}

	resp := VarnishResponse{
		statusCode: status,
		payload:    string(body),
	}

	if status != ClisOk {
		return resp, fmt.Errorf("command '%s' failed with status %d: %s", cmd, status, resp.payload)
	}

	return resp, nil
}

// ExecRaw executes a command and returns the raw status and body.
// Unlike Exec, it does not return an error for non-200 status codes.
func (c *Conn) ExecRaw(cmd string) (status int, body []byte, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return 0, nil, errors.New("connection closed")
	}

	if len(cmd) == 0 {
		return ClisSyntax, nil, errors.New("empty command")
	}

	if err := WriteCommandWithDeadline(c.conn, cmd, c.cmdTimeout); err != nil {
		c.callbacks.invokeError(c, err)
		return ClisComms, nil, fmt.Errorf("write command: %w", err)
	}

	status, body, err = ReadMessageWithDeadline(c.conn, c.cmdTimeout)
	if err != nil {
		c.callbacks.invokeError(c, err)
		return ClisComms, nil, fmt.Errorf("read response: %w", err)
	}

	return status, body, nil
}
