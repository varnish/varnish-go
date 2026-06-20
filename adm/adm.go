// Communicate with a running Varnish instance, the same way the varnishadm command does.
package adm

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// An open connection to varnishd's admin socket.
type Conn struct {
	net.Conn
	versionMutex     sync.Mutex
	cachedVersion *BannerVersion
}

const workdirBase = "/var/lib/varnish"

// withContext runs fn under ctx. It forwards the deadline to the connection and
// spawns a goroutine that expires the connection immediately if the context is
// cancelled mid-I/O. The goroutine is guaranteed to exit before withContext returns.
func (conn *Conn) withContext(ctx context.Context, fn func() error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	deadline, _ := ctx.Deadline() // zero clears deadline
	conn.SetDeadline(deadline)

	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		select {
		case <-ctx.Done():
			select {
			case <-done: // fn already finished; don't clobber deadline
			default:
				conn.SetDeadline(time.Unix(1, 0)) // epoch+1s — always past, unblocks I/O immediately
			}
		case <-done:
		}
	}()
	defer func() {
		close(done)
		wg.Wait()
		if ctx.Err() == nil {
			conn.SetDeadline(time.Time{}) // restore no-deadline only when context is still valid
		}
	}()

	err := fn()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

func findEndpointData(name string) (addrPorts []netip.AddrPort, secretPath string, err error) {
	if name == "" {
		name = "varnishd"
	}
	if name[0] != '/' {
		name = filepath.Join(workdirBase, name)
	}

	p := filepath.Join(name, "_.vsm_mgt", "_.index")
	buf, err := os.ReadFile(p)
	if err != nil {
		return
	}
	// trailing null bytes...
	buf = bytes.Trim(buf, "\x00")

	var SArg string
	var TArg string
	for line := range strings.Lines(string(buf)) {
		fields := strings.Fields(line)
		if len(fields) < 6 ||
			fields[0] != "+" ||
			fields[4] != "Arg" {
			continue
		}
		if fields[5] == "-T" {
			TArg = fields[1]
		} else if fields[5] == "-S" {
			SArg = fields[1]
		}
	}

	p = filepath.Join(name, "_.vsm_mgt", SArg)
	buf, err = os.ReadFile(p)
	if err != nil {
		return
	}
	buf = bytes.Trim(buf, "\x00")
	secretPath = string(buf)

	p = filepath.Join(name, "_.vsm_mgt", TArg)
	buf, err = os.ReadFile(p)
	if err != nil {
		return
	}
	buf = bytes.Trim(buf, "\x00")

	for line := range strings.Lines(string(buf)) {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		var addr netip.Addr
		var port uint64
		addr, err = netip.ParseAddr(fields[0])
		if err != nil {
			return
		}

		port, err = strconv.ParseUint(fields[1], 10, 16)
		if err != nil {
			return
		}
		addrPorts = append(addrPorts, netip.AddrPortFrom(addr, uint16(port)))
	}

	return
}

func (conn *Conn) authenticate(ctx context.Context, secretPath string) (err error) {
	status, nonce, err := conn.ReadMessage(ctx)
	if status != 107 {
		err = fmt.Errorf("status should have been 107")
		conn.Close()
		return
	}
	if len(nonce) < 32 {
		err = fmt.Errorf("nonce too short")
		conn.Close()
		return
	}

	secret, err := os.ReadFile(secretPath)
	if err != nil {
		conn.Close()
		return
	}
	hasher := sha256.New()
	hasher.Write(nonce[:32])
	hasher.Write([]byte("\n"))
	hasher.Write(secret)
	hasher.Write(nonce[:32])
	hasher.Write([]byte("\n"))

	_, err = conn.Ask(ctx, "auth", hex.EncodeToString(hasher.Sum(nil)))
	return
}

// Connect opens a [Conn] using the name of the target Varnish (varnishd's "-n" argument).
func Connect(ctx context.Context, name string) (*Conn, error) {
	addrPorts, secretPath, err := findEndpointData(name)
	if err != nil {
		return nil, err
	}

	if len(addrPorts) == 0 {
		return nil, fmt.Errorf("no available endpoint for %s", name)
	}

	var lastErr error
	for _, addrPort := range addrPorts {
		conn, err := ConnectRaw(ctx, addrPort, secretPath)
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

// ConnectRaw is the same as [Connect], but you need to provide the endpoint and path to the secret file.
// Those correspond to the "-T" and "-S" varnishd arguments respectively.
func ConnectRaw(ctx context.Context, addrPort netip.AddrPort, secretPath string) (*Conn, error) {
	connInner, err := (&net.Dialer{}).DialContext(ctx, "tcp", addrPort.String())
	if err != nil {
		return nil, err
	}
	conn := &Conn{Conn: connInner}
	if err = conn.authenticate(ctx, secretPath); err != nil {
		conn.Close()
		return nil, err
	}
	return conn, nil
}

// Accept is the same as [ConnectRaw] but expects a [net.Listener] that corresponds to the varnishd's "-m" argument.
// ctx cancellation unblocks the Accept call when the listener implements SetDeadline (e.g. *net.TCPListener).
func Accept(ctx context.Context, sock net.Listener, secretPath string) (*Conn, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	dl, _ := sock.(interface{ SetDeadline(time.Time) error })
	var setDeadline bool // true if we ever called dl.SetDeadline; safe to read after wg.Wait()
	if dl != nil {
		if deadline, ok := ctx.Deadline(); ok {
			dl.SetDeadline(deadline)
			setDeadline = true
		}
	}

	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		select {
		case <-ctx.Done():
			select {
			case <-done: // Accept already returned; don't clobber deadline
			default:
				if dl != nil {
					dl.SetDeadline(time.Unix(1, 0)) // epoch+1s — always past, unblocks I/O immediately
					setDeadline = true
				}
			}
		case <-done:
		}
	}()
	defer func() {
		close(done)
		wg.Wait()
		if dl != nil && setDeadline {
			dl.SetDeadline(time.Time{}) // clear deadline; listener may be reused
		}
	}()

	connInner, err := sock.Accept()
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, err
	}
	conn := &Conn{Conn: connInner}
	if err = conn.authenticate(ctx, secretPath); err != nil {
		conn.Close()
		return nil, err
	}
	return conn, nil
}

// readMessageRaw reads one admin protocol message from the wire without context handling.
// Callers are responsible for setting deadlines before calling.
func (conn *Conn) readMessageRaw() (status int, message []byte, err error) {
	sz := 0
	_, err = fmt.Fscanf(conn, "%d %d\n", &status, &sz)
	if err != nil {
		return
	}
	message = make([]byte, sz+1)
	_, err = io.ReadFull(conn, message)
	return
}

// ReadMessage reads the next message from the admin socket.
// Note that you probably only need this if you opened a raw connection to the socket,
// possibly to read the authentication nonce.
func (conn *Conn) ReadMessage(ctx context.Context) (status int, message []byte, err error) {
	err = conn.withContext(ctx, func() error {
		var e error
		status, message, e = conn.readMessageRaw()
		return e
	})
	return
}

// Ask sends a request to the admin socket. It joins all the provided strings with spaces and adds a newline
// before pushing the buffer on the wire. This will error if the status code of the response isn't 200.
func (conn *Conn) Ask(ctx context.Context, args ...string) (message string, err error) {
	command := strings.Join(args, " ") + "\n"
	var status int
	var buf []byte
	err = conn.withContext(ctx, func() error {
		if _, e := conn.Write([]byte(command)); e != nil {
			return e
		}
		var e error
		status, buf, e = conn.readMessageRaw()
		return e
	})
	message = string(buf)
	if err == nil && status != 200 {
		err = fmt.Errorf("command: %sfailed with %d status and message:\n%s", command, status, message)
	}
	return
}

// AskRaw is a lower-level version of [Conn.Ask] giving access to the status code and to the message as [[]byte].
func (conn *Conn) AskRaw(ctx context.Context, args ...string) (status int, message []byte, err error) {
	err = conn.withContext(ctx, func() error {
		if _, e := conn.Write([]byte(strings.Join(args, " ") + "\n")); e != nil {
			return e
		}
		var e error
		status, message, e = conn.readMessageRaw()
		return e
	})
	return
}
