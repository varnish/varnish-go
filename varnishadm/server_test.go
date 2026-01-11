package varnishadm

import (
	"context"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestNewServer(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	server := NewServer(listener, "secret")
	if server == nil {
		t.Fatal("NewServer returned nil")
	}
	if server.listener != listener {
		t.Error("listener not set")
	}
	if string(server.secret) != "secret" {
		t.Error("secret not set")
	}
}

func TestNewServer_WithOptions(t *testing.T) {
	listener, _ := net.Listen("tcp", "127.0.0.1:0")
	defer listener.Close()

	var connectCalled bool
	callbacks := &Callbacks{
		OnConnect: func(*Conn) { connectCalled = true },
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

	server := NewServer(listener, "secret",
		WithServerCallbacks(callbacks),
		WithLogger(logger),
	)

	if server.callbacks != callbacks {
		t.Error("callbacks not set")
	}
	if server.logger != logger {
		t.Error("logger not set")
	}

	// Trigger callback to verify it's wired up
	server.callbacks.invokeConnect(nil)
	if !connectCalled {
		t.Error("callback not called")
	}
}

func TestNewServerFromSecretFile(t *testing.T) {
	listener, _ := net.Listen("tcp", "127.0.0.1:0")
	defer listener.Close()

	// Create temp secret file
	tmpDir := t.TempDir()
	secretPath := filepath.Join(tmpDir, "secret")
	if err := os.WriteFile(secretPath, []byte("filesecret\n"), 0600); err != nil {
		t.Fatalf("write secret: %v", err)
	}

	server, err := NewServerFromSecretFile(listener, secretPath)
	if err != nil {
		t.Fatalf("NewServerFromSecretFile: %v", err)
	}
	if string(server.secret) != "filesecret" {
		t.Errorf("secret = %q, want filesecret", server.secret)
	}
}

func TestNewServerFromSecretFile_NoFile(t *testing.T) {
	listener, _ := net.Listen("tcp", "127.0.0.1:0")
	defer listener.Close()

	_, err := NewServerFromSecretFile(listener, "/nonexistent/secret")
	if err == nil {
		t.Error("expected error for missing file")
	}
}

func TestServer_Accept(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	secret := "testsecret"
	server := NewServer(listener, secret)

	// Simulate varnishd connecting
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		conn, err := net.Dial("tcp", listener.Addr().String())
		if err != nil {
			t.Logf("dial error: %v", err)
			return
		}
		defer conn.Close()

		// Act as varnishd - send challenge, validate auth, send banner
		fake := newFakeVarnishd(conn, secret)
		if err := fake.DoAuth(); err != nil {
			t.Logf("fake auth error: %v", err)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	conn, err := server.Accept(ctx)
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	if conn == nil {
		t.Fatal("conn is nil")
	}
	if conn.Mode() != ModeServer {
		t.Errorf("Mode() = %v, want ModeServer", conn.Mode())
	}

	conn.Close()
	wg.Wait()
}

func TestServer_Accept_ContextCancelled(t *testing.T) {
	listener, _ := net.Listen("tcp", "127.0.0.1:0")
	defer listener.Close()

	server := NewServer(listener, "secret")

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	_, err := server.Accept(ctx)
	if err == nil {
		t.Error("expected error when context cancelled")
	}
}

func TestServer_Accept_Callbacks(t *testing.T) {
	listener, _ := net.Listen("tcp", "127.0.0.1:0")
	defer listener.Close()

	secret := "testsecret"
	var connectCalled bool
	var connectedConn *Conn

	server := NewServer(listener, secret,
		WithServerCallbacks(&Callbacks{
			OnConnect: func(c *Conn) {
				connectCalled = true
				connectedConn = c
			},
		}),
	)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		conn, _ := net.Dial("tcp", listener.Addr().String())
		if conn != nil {
			defer conn.Close()
			fake := newFakeVarnishd(conn, secret)
			fake.DoAuth()
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	conn, err := server.Accept(ctx)
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}

	if !connectCalled {
		t.Error("OnConnect not called")
	}
	if connectedConn != conn {
		t.Error("OnConnect received different conn")
	}

	conn.Close()
	wg.Wait()
}

func TestServer_Accept_AuthFail(t *testing.T) {
	listener, _ := net.Listen("tcp", "127.0.0.1:0")
	defer listener.Close()

	var authFailCalled bool
	var authFailAddr string

	server := NewServer(listener, "correct",
		WithServerCallbacks(&Callbacks{
			OnAuthFail: func(addr string, err error) {
				authFailCalled = true
				authFailAddr = addr
			},
		}),
	)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		conn, _ := net.Dial("tcp", listener.Addr().String())
		if conn != nil {
			defer conn.Close()
			// Use wrong secret
			fake := newFakeVarnishd(conn, "wrong")
			fake.DoAuth() // Will fail
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err := server.Accept(ctx)
	if err == nil {
		t.Error("expected auth failure")
	}

	wg.Wait()

	if !authFailCalled {
		t.Error("OnAuthFail not called")
	}
	if authFailAddr == "" {
		t.Error("authFailAddr is empty")
	}
}

func TestServer_Connections(t *testing.T) {
	listener, _ := net.Listen("tcp", "127.0.0.1:0")
	defer listener.Close()

	server := NewServer(listener, "secret")

	if server.Connections() != 0 {
		t.Errorf("initial connections = %d, want 0", server.Connections())
	}
}

func TestServer_Shutdown(t *testing.T) {
	listener, _ := net.Listen("tcp", "127.0.0.1:0")

	server := NewServer(listener, "secret")

	ctx := context.Background()
	err := server.Shutdown(ctx)
	if err != nil {
		t.Errorf("Shutdown: %v", err)
	}

	// Listener should be closed
	_, err = net.Dial("tcp", listener.Addr().String())
	if err == nil {
		t.Error("expected dial to fail after shutdown")
	}
}

func TestServer_Run(t *testing.T) {
	listener, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := listener.Addr().String()

	secret := "testsecret"
	var connectCount int
	var mu sync.Mutex

	server := NewServer(listener, secret,
		WithServerCallbacks(&Callbacks{
			OnConnect: func(*Conn) {
				mu.Lock()
				connectCount++
				mu.Unlock()
			},
		}),
	)

	ctx, cancel := context.WithCancel(context.Background())

	// Run server in background
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		server.Run(ctx)
	}()

	// Give server time to start
	time.Sleep(50 * time.Millisecond)

	// Connect twice
	for i := 0; i < 2; i++ {
		conn, err := net.Dial("tcp", addr)
		if err != nil {
			t.Logf("dial %d: %v", i, err)
			continue
		}
		fake := newFakeVarnishd(conn, secret)
		fake.DoAuth()
		conn.Close()
		time.Sleep(50 * time.Millisecond)
	}

	cancel()
	wg.Wait()

	mu.Lock()
	count := connectCount
	mu.Unlock()

	if count < 1 {
		t.Errorf("connectCount = %d, want >= 1", count)
	}
}
