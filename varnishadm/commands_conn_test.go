package varnishadm

import (
	"net"
	"strings"
	"sync"
	"testing"
)

// setupTestConn creates a Conn connected to a fake varnishd.
// Returns the conn and a cleanup function.
// The fake varnishd records commands received.
func setupTestConn(t *testing.T, responses map[string]fakeResponse) (*Conn, *[]string, func()) {
	t.Helper()

	client, server := net.Pipe()
	secret := "test"
	var commands []string
	var mu sync.Mutex

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer server.Close()

		fake := newFakeVarnishd(server, secret)

		// Add custom responses
		for cmd, resp := range responses {
			fake.SetResponse(cmd, resp.status, resp.body)
		}

		if err := fake.DoAuth(); err != nil {
			return
		}

		// Handle commands until connection closes
		for {
			cmd, err := fake.HandleCommand()
			if err != nil {
				return
			}
			mu.Lock()
			commands = append(commands, cmd)
			mu.Unlock()
		}
	}()

	auth, err := Authenticate(client, []byte(secret))
	if err != nil {
		server.Close()
		wg.Wait()
		t.Fatalf("auth failed: %v", err)
	}

	conn := newConn(client, ModeClient, auth)

	cleanup := func() {
		conn.Close()
		wg.Wait()
	}

	return conn, &commands, cleanup
}

func TestConn_StandardCommands(t *testing.T) {
	conn, commands, cleanup := setupTestConn(t, map[string]fakeResponse{
		"start":       {ClisOk, "Started"},
		"stop":        {ClisOk, "Stopped"},
		"panic.show":  {ClisOk, "No panic"},
		"panic.clear": {ClisOk, "Cleared"},
	})
	defer cleanup()

	tests := []struct {
		name    string
		call    func() (VarnishResponse, error)
		wantCmd string
	}{
		{"Ping", conn.Ping, "ping"},
		{"Status", conn.Status, "status"},
		{"Start", conn.Start, "start"},
		{"Stop", conn.Stop, "stop"},
		{"PanicShow", conn.PanicShow, "panic.show"},
		{"PanicClear", conn.PanicClear, "panic.clear"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := tt.call()
			if err != nil {
				t.Fatalf("error: %v", err)
			}
			if resp.StatusCode() != ClisOk {
				t.Errorf("status = %d, want %d", resp.StatusCode(), ClisOk)
			}

			// Check command was sent
			found := false
			for _, cmd := range *commands {
				if cmd == tt.wantCmd {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("command %q not found in %v", tt.wantCmd, *commands)
			}
		})
	}
}

func TestConn_VCLCommands(t *testing.T) {
	responses := map[string]fakeResponse{
		"vcl.list":           {ClisOk, "active auto/warm - boot"},
		"vcl.load test /tmp": {ClisOk, "VCL compiled"},
		"vcl.use test":       {ClisOk, "VCL 'test' now active"},
		"vcl.label lbl test": {ClisOk, "Label set"},
		"vcl.discard old":    {ClisOk, "VCL discarded"},
	}

	conn, _, cleanup := setupTestConn(t, responses)
	defer cleanup()

	t.Run("VCLList", func(t *testing.T) {
		resp, err := conn.VCLList()
		if err != nil {
			t.Fatalf("error: %v", err)
		}
		if !strings.Contains(resp.Payload(), "boot") {
			t.Errorf("unexpected payload: %s", resp.Payload())
		}
	})

	t.Run("VCLLoad", func(t *testing.T) {
		resp, err := conn.VCLLoad("test", "/tmp")
		if err != nil {
			t.Fatalf("error: %v", err)
		}
		if !strings.Contains(resp.Payload(), "compiled") {
			t.Errorf("unexpected payload: %s", resp.Payload())
		}
	})

	t.Run("VCLUse", func(t *testing.T) {
		resp, err := conn.VCLUse("test")
		if err != nil {
			t.Fatalf("error: %v", err)
		}
		if !strings.Contains(resp.Payload(), "active") {
			t.Errorf("unexpected payload: %s", resp.Payload())
		}
	})

	t.Run("VCLLabel", func(t *testing.T) {
		resp, err := conn.VCLLabel("lbl", "test")
		if err != nil {
			t.Fatalf("error: %v", err)
		}
		if resp.StatusCode() != ClisOk {
			t.Errorf("status = %d, want %d", resp.StatusCode(), ClisOk)
		}
	})

	t.Run("VCLDiscard", func(t *testing.T) {
		resp, err := conn.VCLDiscard("old")
		if err != nil {
			t.Fatalf("error: %v", err)
		}
		if resp.StatusCode() != ClisOk {
			t.Errorf("status = %d, want %d", resp.StatusCode(), ClisOk)
		}
	})
}

func TestConn_VCLInline(t *testing.T) {
	client, server := net.Pipe()
	secret := "test"

	var receivedCmd string
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer server.Close()

		fake := newFakeVarnishd(server, secret)
		fake.DoAuth()

		// Read the vcl.inline command
		cmd, _ := fake.ReadCommand()
		receivedCmd = cmd
		fake.SendResponse(ClisOk, "VCL compiled")
	}()

	auth, _ := Authenticate(client, []byte(secret))
	conn := newConn(client, ModeClient, auth)

	vcl := `vcl 4.1;
backend default { .host = "localhost"; }`

	_, err := conn.VCLInline("test", vcl)
	if err != nil {
		t.Fatalf("error: %v", err)
	}

	conn.Close()
	wg.Wait()

	if !strings.HasPrefix(receivedCmd, "vcl.inline test") {
		t.Errorf("unexpected command: %s", receivedCmd)
	}
	if !strings.Contains(receivedCmd, "backend default") {
		t.Errorf("VCL content not in command: %s", receivedCmd)
	}
}

func TestConn_VCLListStructured(t *testing.T) {
	responses := map[string]fakeResponse{
		"vcl.list": {ClisOk, `active      auto/warm          - vcl-main (1 label)
available   auto/warm          - vcl-old`},
	}

	conn, _, cleanup := setupTestConn(t, responses)
	defer cleanup()

	result, err := conn.VCLListStructured()
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if len(result.Entries) != 2 {
		t.Errorf("got %d entries, want 2", len(result.Entries))
	}
	if result.Entries[0].Name != "vcl-main" {
		t.Errorf("first entry name = %q, want vcl-main", result.Entries[0].Name)
	}
}

func TestConn_ParamCommands(t *testing.T) {
	responses := map[string]fakeResponse{
		"param.show":             {ClisOk, "all parameters"},
		"param.show default_ttl": {ClisOk, "default_ttl = 120"},
		"param.set default_ttl 60": {ClisOk, ""},
	}

	conn, commands, cleanup := setupTestConn(t, responses)
	defer cleanup()

	t.Run("ParamShow_All", func(t *testing.T) {
		resp, err := conn.ParamShow("")
		if err != nil {
			t.Fatalf("error: %v", err)
		}
		if !strings.Contains(resp.Payload(), "parameters") {
			t.Error("unexpected payload")
		}
	})

	t.Run("ParamShow_Named", func(t *testing.T) {
		resp, err := conn.ParamShow("default_ttl")
		if err != nil {
			t.Fatalf("error: %v", err)
		}
		if !strings.Contains(resp.Payload(), "default_ttl") {
			t.Error("unexpected payload")
		}
	})

	t.Run("ParamSet", func(t *testing.T) {
		_, err := conn.ParamSet("default_ttl", "60")
		if err != nil {
			t.Fatalf("error: %v", err)
		}
		if !containsPrefix(*commands, "param.set") {
			t.Error("param.set command not sent")
		}
	})
}

func TestConn_TLSCommands(t *testing.T) {
	responses := map[string]fakeResponse{
		"tls.cert.list":                   {ClisOk, "Frontend State Hostname\nmain active example.com"},
		"tls.cert.load cert1 /tmp/c.pem":  {ClisOk, "Loaded"},
		"tls.cert.load cert2 /tmp/c.pem -k /tmp/k.pem": {ClisOk, "Loaded"},
		"tls.cert.discard cert1":          {ClisOk, "Discarded"},
		"tls.cert.commit":                 {ClisOk, "Committed"},
		"tls.cert.rollback":               {ClisOk, "Rolled back"},
		"tls.cert.reload":                 {ClisOk, "Reloaded"},
	}

	conn, commands, cleanup := setupTestConn(t, responses)
	defer cleanup()

	t.Run("TLSCertList", func(t *testing.T) {
		resp, err := conn.TLSCertList()
		if err != nil {
			t.Fatalf("error: %v", err)
		}
		if !strings.Contains(resp.Payload(), "Frontend") {
			t.Error("unexpected payload")
		}
	})

	t.Run("TLSCertLoad_Combined", func(t *testing.T) {
		_, err := conn.TLSCertLoad("cert1", "/tmp/c.pem", "")
		if err != nil {
			t.Fatalf("error: %v", err)
		}
		if !containsPrefix(*commands, "tls.cert.load") {
			t.Error("tls.cert.load command not sent")
		}
	})

	t.Run("TLSCertLoad_Separate", func(t *testing.T) {
		_, err := conn.TLSCertLoad("cert2", "/tmp/c.pem", "/tmp/k.pem")
		if err != nil {
			t.Fatalf("error: %v", err)
		}
	})

	t.Run("TLSCertDiscard", func(t *testing.T) {
		_, err := conn.TLSCertDiscard("cert1")
		if err != nil {
			t.Fatalf("error: %v", err)
		}
		if !containsPrefix(*commands, "tls.cert.discard") {
			t.Error("tls.cert.discard command not sent")
		}
	})

	t.Run("TLSCertCommit", func(t *testing.T) {
		_, err := conn.TLSCertCommit()
		if err != nil {
			t.Fatalf("error: %v", err)
		}
	})

	t.Run("TLSCertRollback", func(t *testing.T) {
		_, err := conn.TLSCertRollback()
		if err != nil {
			t.Fatalf("error: %v", err)
		}
	})

	t.Run("TLSCertReload", func(t *testing.T) {
		_, err := conn.TLSCertReload()
		if err != nil {
			t.Fatalf("error: %v", err)
		}
	})
}

func TestConn_TLSCertListStructured(t *testing.T) {
	responses := map[string]fakeResponse{
		"tls.cert.list": {ClisOk, `Frontend State   Hostname         Certificate ID  Expiration date           OCSP stapling
main     active  example.com      cert-001        Jan 02 15:04:05 2030 UTC  true`},
	}

	conn, _, cleanup := setupTestConn(t, responses)
	defer cleanup()

	result, err := conn.TLSCertListStructured()
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if len(result.Entries) != 1 {
		t.Errorf("got %d entries, want 1", len(result.Entries))
	}
}

// containsPrefix checks if any string in the slice has the given prefix
func containsPrefix(slice []string, prefix string) bool {
	for _, s := range slice {
		if strings.HasPrefix(s, prefix) {
			return true
		}
	}
	return false
}
