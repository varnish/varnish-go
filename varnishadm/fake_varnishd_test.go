package varnishadm

import (
	"fmt"
	"io"
	"net"
	"sync"
)

// fakeVarnishd simulates the varnishd side of the CLI protocol for testing.
type fakeVarnishd struct {
	conn      net.Conn
	secret    string
	banner    string
	responses map[string]fakeResponse
	mu        sync.Mutex
}

type fakeResponse struct {
	status int
	body   string
}

// newFakeVarnishd creates a fake varnishd that speaks the CLI protocol.
func newFakeVarnishd(conn net.Conn, secret string) *fakeVarnishd {
	return &fakeVarnishd{
		conn:   conn,
		secret: secret,
		banner: "varnish-test revision abc123\nLinux,5.0.0,x86_64,-junix,-sdefault,-hcritbit\n",
		responses: map[string]fakeResponse{
			"ping":   {ClisOk, "PONG 1234567890 1.0"},
			"status": {ClisOk, "Child in state running"},
		},
	}
}

// SetResponse configures a response for a command.
func (f *fakeVarnishd) SetResponse(cmd string, status int, body string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.responses[cmd] = fakeResponse{status, body}
}

// SendResponse sends a response in Varnish CLI format.
func (f *fakeVarnishd) SendResponse(status int, body string) error {
	// Format: "SSS LLLLLLLL\n" + body + "\n"
	header := fmt.Sprintf("%03d %8d\n", status, len(body))
	_, err := f.conn.Write([]byte(header + body + "\n"))
	return err
}

// ReadCommand reads a command from the connection.
func (f *fakeVarnishd) ReadCommand() (string, error) {
	buf := make([]byte, 4096)
	n, err := f.conn.Read(buf)
	if err != nil {
		return "", err
	}
	// Strip trailing newline
	cmd := string(buf[:n])
	if len(cmd) > 0 && cmd[len(cmd)-1] == '\n' {
		cmd = cmd[:len(cmd)-1]
	}
	return cmd, nil
}

// DoAuth performs the authentication handshake as varnishd would.
// Sends challenge, reads auth response, validates, sends banner.
func (f *fakeVarnishd) DoAuth() error {
	// Send auth challenge (status 107)
	challenge := "abcdefghijklmnopqrstuvwxyz012345" // 32 chars
	if err := f.SendResponse(ClisAuth, challenge); err != nil {
		return fmt.Errorf("send challenge: %w", err)
	}

	// Read auth response
	cmd, err := f.ReadCommand()
	if err != nil {
		return fmt.Errorf("read auth: %w", err)
	}

	// Validate: should be "auth <hex>"
	var authHex string
	if _, err := fmt.Sscanf(cmd, "auth %s", &authHex); err != nil {
		return fmt.Errorf("parse auth command: %w", err)
	}

	// Compute expected response
	expected := ComputeAuthResponse([]byte(challenge), []byte(f.secret))
	if authHex != expected {
		if err := f.SendResponse(ClisCant, "Authentication failed"); err != nil {
			return err
		}
		return fmt.Errorf("auth mismatch: got %s, want %s", authHex, expected)
	}

	// Send success with banner
	return f.SendResponse(ClisOk, f.banner)
}

// HandleCommand reads one command and sends the configured response.
// Returns the command that was received.
func (f *fakeVarnishd) HandleCommand() (string, error) {
	cmd, err := f.ReadCommand()
	if err != nil {
		return "", err
	}

	f.mu.Lock()
	resp, ok := f.responses[cmd]
	f.mu.Unlock()

	if !ok {
		// Unknown command
		if err := f.SendResponse(ClisUnknown, fmt.Sprintf("Unknown request: %s", cmd)); err != nil {
			return cmd, err
		}
		return cmd, nil
	}

	if err := f.SendResponse(resp.status, resp.body); err != nil {
		return cmd, err
	}
	return cmd, nil
}

// HandleCommands reads commands in a loop until the connection closes.
func (f *fakeVarnishd) HandleCommands() error {
	for {
		_, err := f.HandleCommand()
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
	}
}

// Serve runs the full varnishd simulation: auth + command loop.
func (f *fakeVarnishd) Serve() error {
	if err := f.DoAuth(); err != nil {
		return err
	}
	return f.HandleCommands()
}

// simulateVarnishdClient simulates varnishd connecting to a -M listener.
// It connects, performs auth as the client side (varnishd), then handles commands.
func simulateVarnishdClient(addr string, secret string) error {
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		return err
	}
	defer conn.Close()

	fake := newFakeVarnishd(conn, secret)
	return fake.Serve()
}
