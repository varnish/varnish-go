package varnishadm

import (
	"net"
	"sync"
	"testing"
	"time"
)

func TestConnectionMode_String(t *testing.T) {
	tests := []struct {
		mode ConnectionMode
		want string
	}{
		{ModeClient, "client"},
		{ModeServer, "server"},
		{ConnectionMode(99), "unknown"},
	}

	for _, tt := range tests {
		got := tt.mode.String()
		if got != tt.want {
			t.Errorf("ConnectionMode(%d).String() = %q, want %q", tt.mode, got, tt.want)
		}
	}
}

func TestConn_Getters(t *testing.T) {
	client, server := net.Pipe()
	defer server.Close()

	auth := &AuthInfo{
		Banner:      "test banner",
		Environment: "Linux,test",
		Version:     "varnish-test",
	}

	conn := newConn(client, ModeClient, auth)
	defer conn.Close()

	if conn.Mode() != ModeClient {
		t.Errorf("Mode() = %v, want %v", conn.Mode(), ModeClient)
	}
	if conn.Banner() != "test banner" {
		t.Errorf("Banner() = %q, want %q", conn.Banner(), "test banner")
	}
	if conn.Environment() != "Linux,test" {
		t.Errorf("Environment() = %q, want %q", conn.Environment(), "Linux,test")
	}
	if conn.Version() != "varnish-test" {
		t.Errorf("Version() = %q, want %q", conn.Version(), "varnish-test")
	}
	if conn.LocalAddr() == nil {
		t.Error("LocalAddr() is nil")
	}
	if conn.RemoteAddr() == nil {
		t.Error("RemoteAddr() is nil")
	}
}

func TestConn_Options(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	var connectCalled bool
	callbacks := &Callbacks{
		OnConnect: func(*Conn) { connectCalled = true },
	}

	auth := &AuthInfo{}
	conn := newConn(client, ModeClient, auth,
		WithConnCallbacks(callbacks),
		WithCommandTimeout(5*time.Second),
	)

	if conn.callbacks != callbacks {
		t.Error("WithConnCallbacks not applied")
	}
	if conn.cmdTimeout != 5*time.Second {
		t.Errorf("cmdTimeout = %v, want %v", conn.cmdTimeout, 5*time.Second)
	}

	// Manually trigger to test callback was set
	conn.callbacks.invokeConnect(conn)
	if !connectCalled {
		t.Error("callback not called")
	}
}

func TestConn_Close(t *testing.T) {
	client, server := net.Pipe()
	defer server.Close()

	auth := &AuthInfo{}
	conn := newConn(client, ModeClient, auth)

	if conn.IsClosed() {
		t.Error("new conn should not be closed")
	}

	err := conn.Close()
	if err != nil {
		t.Errorf("Close() error: %v", err)
	}

	if !conn.IsClosed() {
		t.Error("conn should be closed after Close()")
	}

	// Close again should be no-op
	err = conn.Close()
	if err != nil {
		t.Errorf("second Close() error: %v", err)
	}
}

func TestConn_Close_Callback(t *testing.T) {
	client, server := net.Pipe()
	defer server.Close()

	var disconnectCalled bool
	callbacks := &Callbacks{
		OnDisconnect: func(*Conn, error) { disconnectCalled = true },
	}

	auth := &AuthInfo{}
	conn := newConn(client, ModeClient, auth, WithConnCallbacks(callbacks))

	conn.Close()

	if !disconnectCalled {
		t.Error("OnDisconnect not called on Close")
	}
}

func TestConn_Exec(t *testing.T) {
	client, server := net.Pipe()
	secret := "testsecret"

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer server.Close()
		fake := newFakeVarnishd(server, secret)
		fake.DoAuth()
		fake.HandleCommand() // ping
	}()

	auth, err := Authenticate(client, []byte(secret))
	if err != nil {
		t.Fatalf("auth failed: %v", err)
	}

	conn := newConn(client, ModeClient, auth)

	resp, err := conn.Exec("ping")
	if err != nil {
		t.Fatalf("Exec failed: %v", err)
	}
	if resp.StatusCode() != ClisOk {
		t.Errorf("status = %d, want %d", resp.StatusCode(), ClisOk)
	}
	if resp.Payload() == "" {
		t.Error("payload is empty")
	}

	conn.Close()
	wg.Wait()
}

func TestConn_Exec_EmptyCommand(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	auth := &AuthInfo{}
	conn := newConn(client, ModeClient, auth)

	_, err := conn.Exec("")
	if err == nil {
		t.Error("expected error for empty command")
	}
}

func TestConn_Exec_Closed(t *testing.T) {
	client, server := net.Pipe()
	defer server.Close()

	auth := &AuthInfo{}
	conn := newConn(client, ModeClient, auth)
	conn.Close()

	_, err := conn.Exec("ping")
	if err == nil {
		t.Error("expected error on closed conn")
	}
}

func TestConn_Exec_ErrorStatus(t *testing.T) {
	client, server := net.Pipe()
	secret := "testsecret"

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer server.Close()
		fake := newFakeVarnishd(server, secret)
		fake.SetResponse("badcmd", ClisCant, "Command failed")
		fake.DoAuth()
		fake.HandleCommand() // badcmd
	}()

	auth, _ := Authenticate(client, []byte(secret))
	conn := newConn(client, ModeClient, auth)

	_, err := conn.Exec("badcmd")
	if err == nil {
		t.Error("expected error for failed command")
	}

	conn.Close()
	wg.Wait()
}

func TestConn_ExecRaw(t *testing.T) {
	client, server := net.Pipe()
	secret := "testsecret"

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer server.Close()
		fake := newFakeVarnishd(server, secret)
		fake.SetResponse("custom", 201, "truncated")
		fake.DoAuth()
		fake.HandleCommand()
	}()

	auth, _ := Authenticate(client, []byte(secret))
	conn := newConn(client, ModeClient, auth)

	status, body, err := conn.ExecRaw("custom")
	if err != nil {
		t.Fatalf("ExecRaw failed: %v", err)
	}
	if status != 201 {
		t.Errorf("status = %d, want 201", status)
	}
	if string(body) != "truncated" {
		t.Errorf("body = %q, want %q", body, "truncated")
	}

	conn.Close()
	wg.Wait()
}

func TestConn_ExecRaw_EmptyCommand(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	auth := &AuthInfo{}
	conn := newConn(client, ModeClient, auth)

	_, _, err := conn.ExecRaw("")
	if err == nil {
		t.Error("expected error for empty command")
	}
}

func TestConn_ExecRaw_Closed(t *testing.T) {
	client, server := net.Pipe()
	defer server.Close()

	auth := &AuthInfo{}
	conn := newConn(client, ModeClient, auth)
	conn.Close()

	_, _, err := conn.ExecRaw("ping")
	if err == nil {
		t.Error("expected error on closed conn")
	}
}

func TestConn_Exec_ErrorCallback(t *testing.T) {
	client, server := net.Pipe()

	var errorCalled bool
	callbacks := &Callbacks{
		OnError: func(*Conn, error) { errorCalled = true },
	}

	auth := &AuthInfo{}
	conn := newConn(client, ModeClient, auth, WithConnCallbacks(callbacks))

	// Close server to cause read error
	server.Close()

	conn.Exec("ping")

	if !errorCalled {
		t.Error("OnError callback not called on error")
	}
}
