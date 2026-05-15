package varnishadm

import (
	"bytes"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const workdirBase = "/var/lib/varnish"

// Connect connects to a running varnishd by name.
// The name corresponds to varnishd's "-n" argument.
// If name is empty, it defaults to "varnishd".
// If name doesn't start with "/", it's looked up under /var/lib/varnish/.
func Connect(name string, opts ...ConnOption) (*Conn, error) {
	addrPorts, secretPath, err := findEndpointData(name)
	if err != nil {
		return nil, fmt.Errorf("find endpoint data for %q: %w", name, err)
	}

	if len(addrPorts) == 0 {
		return nil, fmt.Errorf("no available endpoint for %q", name)
	}

	var lastErr error
	for _, addrPort := range addrPorts {
		conn, err := ConnectRaw(addrPort.String(), secretPath, opts...)
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}

	return nil, lastErr
}

// ConnectRaw connects to varnishd with explicit endpoint and secret path.
// addr should be in "host:port" format.
// secretPath is the path to the secret file (varnishd's -S argument).
func ConnectRaw(addr string, secretPath string, opts ...ConnOption) (*Conn, error) {
	netConn, err := net.Dial("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", addr, err)
	}

	auth, err := AuthenticateFromFile(netConn, secretPath)
	if err != nil {
		netConn.Close()
		return nil, fmt.Errorf("authenticate: %w", err)
	}

	conn := newConn(netConn, ModeClient, auth, opts...)
	conn.callbacks.invokeConnect(conn)

	return conn, nil
}

// findEndpointData discovers the admin socket address and secret path
// by reading the VSM files in the varnishd workdir.
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
		return nil, "", fmt.Errorf("read index file %s: %w", p, err)
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

	// Validate that we found both -T and -S arguments
	if TArg == "" {
		return nil, "", fmt.Errorf("admin address (-T) not found in index file %s", p)
	}
	if SArg == "" {
		return nil, "", fmt.Errorf("secret path (-S) not found in index file %s", p)
	}

	// Read secret path
	p = filepath.Join(name, "_.vsm_mgt", SArg)
	buf, err = os.ReadFile(p)
	if err != nil {
		return nil, "", fmt.Errorf("read secret path from %s: %w", p, err)
	}
	buf = bytes.Trim(buf, "\x00")
	secretPath = string(buf)

	// Read admin socket addresses
	p = filepath.Join(name, "_.vsm_mgt", TArg)
	buf, err = os.ReadFile(p)
	if err != nil {
		return nil, "", fmt.Errorf("read addresses from %s: %w", p, err)
	}
	buf = bytes.Trim(buf, "\x00")

	for line := range strings.Lines(string(buf)) {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		addr, err := netip.ParseAddr(fields[0])
		if err != nil {
			continue
		}

		port, err := strconv.ParseUint(fields[1], 10, 16)
		if err != nil {
			continue
		}
		addrPorts = append(addrPorts, netip.AddrPortFrom(addr, uint16(port)))
	}

	return addrPorts, secretPath, nil
}
