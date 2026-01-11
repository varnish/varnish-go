package varnishadm

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Protocol constants
const (
	headerSize       = 13 // "SSS LLLLLLLL\n"
	defaultIOTimeout = 10 * time.Second
	authTimeout      = 5 * time.Second
)

// ReadMessage reads a single message from the Varnish CLI protocol.
// The protocol format is:
//   - 13 byte header: "SSS LLLLLLLL\n" where SSS is status code, LLLLLLLL is body length
//   - Body of exactly LLLLLLLL bytes
//   - Final newline
func ReadMessage(r io.Reader) (status int, body []byte, err error) {
	reader := bufio.NewReader(r)

	// Read the 13-byte header (including newline)
	header := make([]byte, headerSize)
	n, err := io.ReadFull(reader, header)
	if err != nil {
		return 0, nil, fmt.Errorf("read header: %w", err)
	}
	if n != headerSize {
		return 0, nil, fmt.Errorf("incomplete header: got %d bytes, expected %d", n, headerSize)
	}

	// Validate header format
	if header[3] != ' ' {
		return 0, nil, fmt.Errorf("invalid header format: missing space at position 3")
	}
	if header[12] != '\n' {
		return 0, nil, fmt.Errorf("invalid header format: missing newline at position 12")
	}

	// Parse status code (first 3 bytes)
	statusStr := strings.TrimSpace(string(header[0:3]))
	status, err = strconv.Atoi(statusStr)
	if err != nil {
		return 0, nil, fmt.Errorf("invalid status code '%s': %w", statusStr, err)
	}

	// Parse body length (bytes 4-11)
	lengthStr := strings.TrimSpace(string(header[4:12]))
	bodyLen, err := strconv.Atoi(lengthStr)
	if err != nil {
		return 0, nil, fmt.Errorf("invalid body length '%s': %w", lengthStr, err)
	}

	// Read the body + trailing newline
	bodyWithNewline := make([]byte, bodyLen+1)
	n, err = io.ReadFull(reader, bodyWithNewline)
	if err != nil {
		return 0, nil, fmt.Errorf("read body: %w", err)
	}
	if n != bodyLen+1 {
		return 0, nil, fmt.Errorf("incomplete body: got %d bytes, expected %d", n, bodyLen+1)
	}

	// Validate trailing newline
	if bodyWithNewline[bodyLen] != '\n' {
		return 0, nil, fmt.Errorf("missing trailing newline after body")
	}

	// Remove the trailing newline from body
	body = bodyWithNewline[:bodyLen]

	return status, body, nil
}

// ReadMessageWithDeadline reads a message from a connection with a deadline.
func ReadMessageWithDeadline(conn net.Conn, timeout time.Duration) (status int, body []byte, err error) {
	deadline := time.Now().Add(timeout)
	if err := conn.SetReadDeadline(deadline); err != nil {
		return 0, nil, fmt.Errorf("set read deadline: %w", err)
	}
	return ReadMessage(conn)
}

// WriteCommand writes a command to the Varnish CLI.
func WriteCommand(w io.Writer, cmd string) error {
	_, err := w.Write([]byte(cmd + NewLine))
	return err
}

// WriteCommandWithDeadline writes a command to a connection with a deadline.
func WriteCommandWithDeadline(conn net.Conn, cmd string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	if err := conn.SetWriteDeadline(deadline); err != nil {
		return fmt.Errorf("set write deadline: %w", err)
	}
	return WriteCommand(conn, cmd)
}

// ComputeAuthResponse computes the SHA256 authentication response.
// The formula is: SHA256(challenge + "\n" + secret + challenge + "\n")
func ComputeAuthResponse(challenge []byte, secret []byte) string {
	var buf bytes.Buffer
	buf.Write(challenge)
	buf.WriteString(NewLine)
	buf.Write(secret)
	buf.Write(challenge)
	buf.WriteString(NewLine)

	hash := sha256.Sum256(buf.Bytes())
	return hex.EncodeToString(hash[:])
}

// AuthInfo contains information parsed from a successful authentication.
type AuthInfo struct {
	Banner      string // Full banner text
	Environment string // e.g., "Linux,6.8.0-79-generic,x86_64,-jlinux,-smse4,-hcritbit"
	Version     string // e.g., "varnish-7.7.3 revision ..."
}

// Authenticate performs the Varnish CLI authentication handshake.
// It reads the challenge, computes the response, and verifies authentication.
func Authenticate(conn net.Conn, secret []byte) (*AuthInfo, error) {
	// Read authentication challenge (status 107)
	status, body, err := ReadMessageWithDeadline(conn, authTimeout)
	if err != nil {
		return nil, fmt.Errorf("read auth challenge: %w", err)
	}

	if status != ClisAuth {
		return nil, fmt.Errorf("expected auth challenge (status %d), got status %d: %s",
			ClisAuth, status, strings.TrimSpace(string(body)))
	}

	// Extract challenge from payload (first line)
	lines := strings.Split(string(body), NewLine)
	if len(lines) == 0 || len(lines[0]) < 32 {
		return nil, fmt.Errorf("invalid auth challenge: too short")
	}
	challenge := []byte(lines[0][:32])

	// Compute and send auth response
	authResponse := ComputeAuthResponse(challenge, secret)
	if err := WriteCommandWithDeadline(conn, "auth "+authResponse, authTimeout); err != nil {
		return nil, fmt.Errorf("send auth response: %w", err)
	}

	// Read auth result
	status, body, err = ReadMessageWithDeadline(conn, authTimeout)
	if err != nil {
		return nil, fmt.Errorf("read auth result: %w", err)
	}

	if status != ClisOk {
		return nil, fmt.Errorf("authentication rejected (status %d): %s",
			status, strings.TrimSpace(string(body)))
	}

	// Parse banner information
	banner := string(body)
	env, version := parseBanner(banner)

	return &AuthInfo{
		Banner:      banner,
		Environment: env,
		Version:     version,
	}, nil
}

// ReadSecretFile reads a secret file and trims trailing whitespace.
// This is the standard way to read Varnish secret files.
func ReadSecretFile(path string) ([]byte, error) {
	secret, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read secret file %s: %w", path, err)
	}
	// Trim trailing newlines/carriage returns
	secret = bytes.TrimRight(secret, "\n\r")
	return secret, nil
}

// AuthenticateFromFile reads the secret from a file and authenticates.
func AuthenticateFromFile(conn net.Conn, secretPath string) (*AuthInfo, error) {
	secret, err := ReadSecretFile(secretPath)
	if err != nil {
		return nil, err
	}
	return Authenticate(conn, secret)
}

// parseBanner extracts environment and version information from Varnish CLI banner.
func parseBanner(banner string) (environment, version string) {
	// Extract environment line (e.g., "Linux,6.8.0-79-generic,x86_64,-jlinux,-smse4,-hcritbit")
	envRegex := regexp.MustCompile(`(?m)^([A-Za-z0-9_]+(?:,[^,\r\n]+)+)\s*$`)
	if envMatch := envRegex.FindStringSubmatch(banner); len(envMatch) > 1 {
		environment = envMatch[1]
	}

	// Extract version line (e.g., "varnish-plus-6.0.15r1 revision d0b65fce8c712013f9bd614bacca1e67a45799e8")
	versionRegex := regexp.MustCompile(`(varnish-[^\r\n]+)`)
	if versionMatch := versionRegex.FindStringSubmatch(banner); len(versionMatch) > 1 {
		version = versionMatch[1]
	}

	return
}
