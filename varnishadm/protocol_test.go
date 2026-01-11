package varnishadm

import (
	"bytes"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReadMessage(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantStatus int
		wantBody   string
		wantErr    bool
	}{
		{
			name:       "simple message",
			input:      "200        5\nhello\n",
			wantStatus: 200,
			wantBody:   "hello",
		},
		{
			name:       "auth challenge",
			input:      "107       32\nabcdefghijklmnopqrstuvwxyz012345\n",
			wantStatus: 107,
			wantBody:   "abcdefghijklmnopqrstuvwxyz012345",
		},
		{
			name:       "empty body",
			input:      "200        0\n\n",
			wantStatus: 200,
			wantBody:   "",
		},
		{
			name:       "error status",
			input:      "300       11\nSome error\n\n",
			wantStatus: 300,
			wantBody:   "Some error\n",
		},
		{
			name:       "multiline body",
			input:      "200       12\nline1\nline2\n\n",
			wantStatus: 200,
			wantBody:   "line1\nline2\n",
		},
		{
			name:    "short header",
			input:   "200\n",
			wantErr: true,
		},
		{
			name:    "missing space",
			input:   "200X       5\nhello\n",
			wantErr: true,
		},
		{
			name:    "missing header newline",
			input:   "200        5Xhello\n",
			wantErr: true,
		},
		{
			name:    "invalid status",
			input:   "XXX        5\nhello\n",
			wantErr: true,
		},
		{
			name:    "invalid length",
			input:   "200 XXXXXXXX\nhello\n",
			wantErr: true,
		},
		{
			name:    "truncated body",
			input:   "200       10\nhello\n",
			wantErr: true,
		},
		{
			name:    "missing trailing newline",
			input:   "200        5\nhelloX",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, body, err := ReadMessage(strings.NewReader(tt.input))
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if status != tt.wantStatus {
				t.Errorf("status = %d, want %d", status, tt.wantStatus)
			}
			if string(body) != tt.wantBody {
				t.Errorf("body = %q, want %q", body, tt.wantBody)
			}
		})
	}
}

func TestReadMessageWithDeadline(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	// Write a valid message from server side
	go func() {
		server.Write([]byte("200        4\nPONG\n"))
	}()

	status, body, err := ReadMessageWithDeadline(client, time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != 200 {
		t.Errorf("status = %d, want 200", status)
	}
	if string(body) != "PONG" {
		t.Errorf("body = %q, want PONG", body)
	}
}

func TestReadMessageWithDeadline_Timeout(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	// Don't write anything - should timeout
	_, _, err := ReadMessageWithDeadline(client, 10*time.Millisecond)
	if err == nil {
		t.Error("expected timeout error, got nil")
	}
}

func TestWriteCommand(t *testing.T) {
	var buf bytes.Buffer
	err := WriteCommand(&buf, "ping")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if buf.String() != "ping\n" {
		t.Errorf("got %q, want %q", buf.String(), "ping\n")
	}
}

func TestWriteCommandWithDeadline(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	// Read from server side to prevent blocking
	go func() {
		buf := make([]byte, 100)
		server.Read(buf)
	}()

	err := WriteCommandWithDeadline(client, "status", time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestComputeAuthResponse(t *testing.T) {
	// Known values - can verify against actual varnishd
	challenge := []byte("abcdefghijklmnopqrstuvwxyz012345")
	secret := []byte("testsecret")

	result := ComputeAuthResponse(challenge, secret)

	// Result should be 64 hex chars (SHA256)
	if len(result) != 64 {
		t.Errorf("result length = %d, want 64", len(result))
	}

	// Should be deterministic
	result2 := ComputeAuthResponse(challenge, secret)
	if result != result2 {
		t.Error("ComputeAuthResponse not deterministic")
	}

	// Different secret should give different result
	result3 := ComputeAuthResponse(challenge, []byte("other"))
	if result == result3 {
		t.Error("different secrets produced same result")
	}
}

func TestAuthenticate(t *testing.T) {
	client, server := net.Pipe()
	secret := "testsecret"

	go func() {
		defer server.Close()
		fake := newFakeVarnishd(server, secret)
		if err := fake.DoAuth(); err != nil {
			t.Logf("fake auth error: %v", err)
		}
	}()

	auth, err := Authenticate(client, []byte(secret))
	client.Close()

	if err != nil {
		t.Fatalf("Authenticate failed: %v", err)
	}
	if auth == nil {
		t.Fatal("auth is nil")
	}
	if auth.Banner == "" {
		t.Error("Banner is empty")
	}
	if auth.Version == "" {
		t.Error("Version is empty")
	}
}

func TestAuthenticate_WrongSecret(t *testing.T) {
	client, server := net.Pipe()

	go func() {
		defer server.Close()
		fake := newFakeVarnishd(server, "correct")
		fake.DoAuth() // Will fail, that's expected
	}()

	_, err := Authenticate(client, []byte("wrong"))
	client.Close()

	if err == nil {
		t.Error("expected auth failure, got nil")
	}
}

func TestAuthenticateFromFile(t *testing.T) {
	// Create temp secret file
	tmpDir := t.TempDir()
	secretPath := filepath.Join(tmpDir, "secret")
	secret := "filesecret"
	if err := os.WriteFile(secretPath, []byte(secret+"\n"), 0600); err != nil {
		t.Fatalf("write secret file: %v", err)
	}

	client, server := net.Pipe()

	go func() {
		defer server.Close()
		fake := newFakeVarnishd(server, secret)
		fake.DoAuth()
	}()

	auth, err := AuthenticateFromFile(client, secretPath)
	client.Close()

	if err != nil {
		t.Fatalf("AuthenticateFromFile failed: %v", err)
	}
	if auth == nil {
		t.Fatal("auth is nil")
	}
}

func TestAuthenticateFromFile_NoFile(t *testing.T) {
	client, _ := net.Pipe()
	defer client.Close()

	_, err := AuthenticateFromFile(client, "/nonexistent/path/secret")
	if err == nil {
		t.Error("expected error for missing file, got nil")
	}
}
