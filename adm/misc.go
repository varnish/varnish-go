package adm

import (
	"context"
	"fmt"
)

// Status returns the running state of the child process (e.g. "running", "stopped").
func (c *Conn) Status(ctx context.Context) (string, error) {
	msg, err := c.Ask(ctx, "status", "-j")
	if err != nil {
		return "", err
	}
	return parseJSONSingle[string](msg)
}

// Ping verifies the connection to varnishd is alive.
func (c *Conn) Ping(ctx context.Context) error {
	_, err := c.Ask(ctx, "ping")
	return err
}

// PIDResponse holds the process IDs of the varnishd master and worker processes.
type PIDResponse struct {
	Master int `json:"master"` // PID of the varnishd management process
	Worker int `json:"worker"` // PID of the cache worker process; 0 if not running
}

// PID returns the PIDs of the varnishd master and worker processes.
func (c *Conn) PID(ctx context.Context) (PIDResponse, error) {
	msg, err := c.Ask(ctx, "pid", "-j")
	if err != nil {
		return PIDResponse{}, err
	}
	return parseJSONSingle[PIDResponse](msg)
}

// Start starts the varnishd cache worker process.
func (c *Conn) Start(ctx context.Context) error {
	_, err := c.Ask(ctx, "start")
	return err
}

// Stop stops the varnishd cache worker process.
func (c *Conn) Stop(ctx context.Context) error {
	_, err := c.Ask(ctx, "stop")
	return err
}

// Banner returns the varnishd welcome banner.
func (c *Conn) Banner(ctx context.Context) (string, error) {
	return c.Ask(ctx, "banner")
}

// Quit closes the admin connection. varnishd responds with status 500 on quit.
func (c *Conn) Quit(ctx context.Context) error {
	status, _, err := c.AskRaw(ctx, "quit")
	if err != nil {
		return err
	}
	if status != 500 {
		return fmt.Errorf("quit: unexpected status %d", status)
	}
	return c.Close()
}

// PanicShow returns the last panic message, or an empty string if none.
// varnishd returns status 300 when no panic has occurred.
func (c *Conn) PanicShow(ctx context.Context) (string, error) {
	status, msg, err := c.AskRaw(ctx, "panic.show", "-j")
	if err != nil {
		return "", err
	}
	if status == 300 {
		return "", nil
	}
	if status != 200 {
		return "", fmt.Errorf("panic.show failed with status %d: %s", status, string(msg))
	}
	return parseJSONSingle[string](string(msg))
}

// PanicClear clears the last panic. If resetCounters is true, related varnishstat counters are also reset.
// varnishd returns status 300 when there is no panic to clear; this is treated as success.
func (c *Conn) PanicClear(ctx context.Context, resetCounters bool) error {
	args := []string{"panic.clear"}
	if resetCounters {
		args = append(args, "-z")
	}
	status, msg, err := c.AskRaw(ctx, args...)
	if err != nil {
		return err
	}
	if status == 300 || status == 200 {
		return nil
	}
	return fmt.Errorf("panic.clear failed with status %d: %s", status, string(msg))
}
