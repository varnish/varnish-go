// Communicate with a running Varnish instance, the same way the varnishadm command does.
package adm

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// An open connection to varnishd's admin socket.
type Conn struct {
	net.Conn
}

const workdirBase = "/var/lib/varnish"

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

func (conn *Conn) authenticate(secretPath string) (err error) {
	status, nonce, err := conn.ReadMessage()
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
		fmt.Printf("arg: %s: %s\n", secretPath, err)
		conn.Close()
		return
	}
	hasher := sha256.New()
	hasher.Write(nonce[:32])
	hasher.Write([]byte("\n"))
	hasher.Write(secret)
	hasher.Write(nonce[:32])
	hasher.Write([]byte("\n"))

	_, err = conn.Ask("auth", hex.EncodeToString(hasher.Sum(nil)))
	return
}

// Open a [Conn] using the name of the target Varnish (varnishd's "-n" argument)
func Connect(name string) (conn Conn, err error) {
	addrPorts, secretPath, err := findEndpointData(name)
	if err != nil {
		return
	}

	if len(addrPorts) == 0 {
		err = fmt.Errorf("no available endpoint for %s", name)
		return
	}

	for _, addrPort := range addrPorts {
		conn, err = ConnectRaw(addrPort, secretPath)
		// if everything went well, return what we have
		if err == nil {
			return
		}
	}
	return
}

// Same as [Connect], but you need to provide the endpoint and path the secret file. Those corresponds to the "-T" and "-S" varnishd's arguments, repectively. It will use secretPath to authenticate the connection.
func ConnectRaw(addrPort netip.AddrPort, secretPath string) (conn Conn, err error) {
	connInner, err := net.Dial("tcp", addrPort.String())
	if err != nil {
		return
	}
	conn = Conn{connInner}
	err = conn.authenticate(secretPath)
	return
}

// Same as [ConnectRaw] but expects a [net.Listener] that corresponds to the varnishd's "-m" argument.
func Accept(sock net.Listener, secretPath string) (conn Conn, err error) {
	connInner, err := sock.Accept()
	if err != nil {
		return
	}
	conn = Conn{connInner}
	err = conn.authenticate(secretPath)
	return
}

// Reads the next message from the admin socket. Note that you probably only need this if you opened a raw connection to the socket, possibly to read the authentication nonce.
func (conn *Conn) ReadMessage() (status int, message []byte, err error) {
	sz := 0
	_, err = fmt.Fscanf(conn, "%d %d\n", &status, &sz)
	if err != nil {
		return
	}
	message = make([]byte, sz+1)

	_, err = conn.Read(message)
	if err != nil {
		return
	}

	return
}

// Send a request to the admin socket. It will join all the provided string with spaces and add a newline before
// pushing the buffer on the wire.
// This will error our if the status code of the response isn't 200
func (conn *Conn) Ask(args ...string) (message string, err error) {
	command := strings.Join(args, " ") + "\n"
	_, err = conn.Write([]byte(command))
	if err != nil {
		return
	}

	status, buf, err := conn.ReadMessage()
	message = string(buf)
	if status != 200 {
		err = fmt.Errorf("command: %sfailed with %d status and message message:\n%s", command, status, message)
	}
	return
}

// A lower-level version of [Conn.Ask] giving access to the status code and to the message as [[]byte]
func (conn *Conn) AskRaw(args ...string) (status int, message []byte, err error) {
	_, err = conn.Write([]byte(strings.Join(args, " ") + "\n"))
	if err != nil {
		return 0, []byte{}, err
	}
	return conn.ReadMessage()
}
