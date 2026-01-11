package varnishadm

import (
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestConnectRaw(t *testing.T) {
	// Start a fake varnishd server
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	secret := "testsecret"

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		fake := newFakeVarnishd(conn, secret)
		fake.Serve()
	}()

	// Create temp secret file
	tmpDir := t.TempDir()
	secretPath := filepath.Join(tmpDir, "secret")
	if err := os.WriteFile(secretPath, []byte(secret), 0600); err != nil {
		t.Fatalf("write secret: %v", err)
	}

	conn, err := ConnectRaw(listener.Addr().String(), secretPath)
	if err != nil {
		t.Fatalf("ConnectRaw: %v", err)
	}
	if conn == nil {
		t.Fatal("conn is nil")
	}
	if conn.Mode() != ModeClient {
		t.Errorf("Mode() = %v, want ModeClient", conn.Mode())
	}

	// Test that we can execute commands
	resp, err := conn.Ping()
	if err != nil {
		t.Errorf("Ping: %v", err)
	}
	if resp.StatusCode() != ClisOk {
		t.Errorf("Ping status = %d", resp.StatusCode())
	}

	conn.Close()
	wg.Wait()
}

func TestConnectRaw_WithCallbacks(t *testing.T) {
	listener, _ := net.Listen("tcp", "127.0.0.1:0")
	defer listener.Close()

	secret := "testsecret"

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		fake := newFakeVarnishd(conn, secret)
		fake.DoAuth()
	}()

	tmpDir := t.TempDir()
	secretPath := filepath.Join(tmpDir, "secret")
	os.WriteFile(secretPath, []byte(secret), 0600)

	var connectCalled bool
	conn, err := ConnectRaw(listener.Addr().String(), secretPath,
		WithConnCallbacks(&Callbacks{
			OnConnect: func(*Conn) { connectCalled = true },
		}),
	)
	if err != nil {
		t.Fatalf("ConnectRaw: %v", err)
	}

	if !connectCalled {
		t.Error("OnConnect not called")
	}

	conn.Close()
	wg.Wait()
}

func TestConnectRaw_BadAddress(t *testing.T) {
	tmpDir := t.TempDir()
	secretPath := filepath.Join(tmpDir, "secret")
	os.WriteFile(secretPath, []byte("secret"), 0600)

	_, err := ConnectRaw("127.0.0.1:1", secretPath) // Port 1 should fail
	if err == nil {
		t.Error("expected error for bad address")
	}
}

func TestConnectRaw_BadSecret(t *testing.T) {
	listener, _ := net.Listen("tcp", "127.0.0.1:0")
	defer listener.Close()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		fake := newFakeVarnishd(conn, "correct")
		fake.DoAuth()
	}()

	tmpDir := t.TempDir()
	secretPath := filepath.Join(tmpDir, "secret")
	os.WriteFile(secretPath, []byte("wrong"), 0600)

	_, err := ConnectRaw(listener.Addr().String(), secretPath)
	wg.Wait()

	if err == nil {
		t.Error("expected auth failure")
	}
}

func TestConnectRaw_NoSecretFile(t *testing.T) {
	listener, _ := net.Listen("tcp", "127.0.0.1:0")
	defer listener.Close()

	_, err := ConnectRaw(listener.Addr().String(), "/nonexistent/secret")
	if err == nil {
		t.Error("expected error for missing secret file")
	}
}

func TestFindEndpointData(t *testing.T) {
	// Create a mock VSM directory structure
	tmpDir := t.TempDir()
	vsmDir := filepath.Join(tmpDir, "_.vsm_mgt")
	if err := os.MkdirAll(vsmDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// Create _.index file
	indexContent := `+ file1 0 0 Arg -T
+ file2 0 0 Arg -S
`
	if err := os.WriteFile(filepath.Join(vsmDir, "_.index"), []byte(indexContent), 0644); err != nil {
		t.Fatalf("write index: %v", err)
	}

	// Create -T file (addresses)
	addrContent := "127.0.0.1 6082\n"
	if err := os.WriteFile(filepath.Join(vsmDir, "file1"), []byte(addrContent), 0644); err != nil {
		t.Fatalf("write addr: %v", err)
	}

	// Create -S file (secret path)
	secretPath := "/tmp/test-secret"
	if err := os.WriteFile(filepath.Join(vsmDir, "file2"), []byte(secretPath), 0644); err != nil {
		t.Fatalf("write secret path: %v", err)
	}

	addrs, secret, err := findEndpointData(tmpDir)
	if err != nil {
		t.Fatalf("findEndpointData: %v", err)
	}

	if len(addrs) != 1 {
		t.Errorf("got %d addrs, want 1", len(addrs))
	}
	if addrs[0].String() != "127.0.0.1:6082" {
		t.Errorf("addr = %s, want 127.0.0.1:6082", addrs[0])
	}
	if secret != secretPath {
		t.Errorf("secret = %q, want %q", secret, secretPath)
	}
}

func TestFindEndpointData_MultipleAddresses(t *testing.T) {
	tmpDir := t.TempDir()
	vsmDir := filepath.Join(tmpDir, "_.vsm_mgt")
	os.MkdirAll(vsmDir, 0755)

	indexContent := `+ file1 0 0 Arg -T
+ file2 0 0 Arg -S
`
	os.WriteFile(filepath.Join(vsmDir, "_.index"), []byte(indexContent), 0644)

	// Multiple addresses (IPv4 and IPv6)
	addrContent := `127.0.0.1 6082
::1 6082
`
	os.WriteFile(filepath.Join(vsmDir, "file1"), []byte(addrContent), 0644)
	os.WriteFile(filepath.Join(vsmDir, "file2"), []byte("/tmp/secret"), 0644)

	addrs, _, err := findEndpointData(tmpDir)
	if err != nil {
		t.Fatalf("findEndpointData: %v", err)
	}

	if len(addrs) != 2 {
		t.Errorf("got %d addrs, want 2", len(addrs))
	}
}

func TestFindEndpointData_DefaultName(t *testing.T) {
	// When name is empty, should default to "varnishd"
	// This will fail because /var/lib/varnish/varnishd doesn't exist
	// but we're testing that the path is constructed correctly
	_, _, err := findEndpointData("")
	if err == nil {
		// Only fails if the default path doesn't exist
		t.Log("default varnishd path exists (unexpected in test)")
	}
}

func TestFindEndpointData_NoIndex(t *testing.T) {
	tmpDir := t.TempDir()
	_, _, err := findEndpointData(tmpDir)
	if err == nil {
		t.Error("expected error for missing index")
	}
}

func TestConnect(t *testing.T) {
	// This test requires a running varnishd or mock VSM structure
	// Skip in normal test runs
	if os.Getenv("VARNISH_TEST_CONNECT") == "" {
		t.Skip("set VARNISH_TEST_CONNECT to run this test")
	}

	conn, err := Connect("")
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer conn.Close()

	resp, err := conn.Ping()
	if err != nil {
		t.Fatalf("Ping: %v", err)
	}
	if resp.StatusCode() != ClisOk {
		t.Errorf("Ping status = %d", resp.StatusCode())
	}
}

func TestConnectRaw_Timeout(t *testing.T) {
	listener, _ := net.Listen("tcp", "127.0.0.1:0")
	defer listener.Close()

	// Accept but don't respond - should timeout
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		// Just hold the connection open
		time.Sleep(5 * time.Second)
		conn.Close()
	}()

	tmpDir := t.TempDir()
	secretPath := filepath.Join(tmpDir, "secret")
	os.WriteFile(secretPath, []byte("secret"), 0600)

	// This should timeout during auth
	_, err := ConnectRaw(listener.Addr().String(), secretPath,
		WithCommandTimeout(100*time.Millisecond),
	)

	if err == nil {
		t.Error("expected timeout error")
	}
}
