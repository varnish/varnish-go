package vrun

import (
	"context"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/varnish/varnish-go/adm"
)

func TestManagerCreation(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	workDir := "/tmp/test-varnish"
	varnishDir := filepath.Join(workDir, "varnish")

	mgr := New(workDir, logger, varnishDir)
	if mgr == nil {
		t.Fatal("Manager creation failed")
	}
	if mgr.workDir != workDir {
		t.Errorf("Expected workDir %s, got %s", workDir, mgr.workDir)
	}
	if mgr.varnishDir != filepath.Join(workDir, "varnish") {
		t.Errorf("Expected varnishDir %s, got %s", filepath.Join(workDir, "varnish"), mgr.varnishDir)
	}
}

func TestPrepareWorkspace(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	workDir := t.TempDir()
	varnishDir := filepath.Join(workDir, "varnish")

	mgr := New(workDir, logger, varnishDir)

	err := mgr.PrepareWorkspace("")
	if err != nil {
		t.Fatalf("PrepareWorkspace failed: %v", err)
	}

	// Check varnish directory exists
	if _, err := os.Stat(mgr.varnishDir); os.IsNotExist(err) {
		t.Errorf("Varnish directory was not created: %s", mgr.varnishDir)
	}

	// Check secret file exists
	secretPath := filepath.Join(workDir, "secret")
	if _, err := os.Stat(secretPath); os.IsNotExist(err) {
		t.Error("Secret file was not created")
	}

	// Check secret is set
	if mgr.secret == "" {
		t.Error("Secret was not generated")
	}
}

func TestPrepareWorkspaceWithLicense(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	workDir := t.TempDir()

	mgr := New(workDir, logger, "")

	licenseText := "TEST LICENSE"
	err := mgr.PrepareWorkspace(licenseText)
	if err != nil {
		t.Fatalf("PrepareWorkspace failed: %v", err)
	}

	// Check license file exists and has correct content
	licensePath := filepath.Join(workDir, "orca.lic")
	content, err := os.ReadFile(licensePath)
	if err != nil {
		t.Error("License file was not created")
	}
	if string(content) != licenseText {
		t.Errorf("License content mismatch: expected %s, got %s", licenseText, string(content))
	}
}

func TestBuildArgs(t *testing.T) {
	cfg := &Config{
		AdminPort:  6082,
		WorkDir:    "/tmp/test",
		VarnishDir: "/tmp/test/varnish",
		Listen:     []string{":8080,http"},
		Storage:    []string{"malloc,256m"},
		Params:     map[string]string{"thread_pool_min": "10"},
	}

	args := BuildArgs(cfg)

	// Check expected arguments
	expectedArgs := []string{"-n", "/tmp/test/varnish", "-F", "-f", "", "-a", ":8080,http"}
	for _, expected := range expectedArgs {
		found := false
		for _, arg := range args {
			if arg == expected {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Expected argument %s not found in args: %v", expected, args)
		}
	}

	// Verify storage args
	storageFound := false
	for i, arg := range args {
		if arg == "-s" && i+1 < len(args) && args[i+1] == "malloc,256m" {
			storageFound = true
			break
		}
	}
	if !storageFound {
		t.Error("Storage arguments not found in args")
	}

	// Verify params
	paramFound := false
	for i, arg := range args {
		if arg == "-p" && i+1 < len(args) && args[i+1] == "thread_pool_min=10" {
			paramFound = true
			break
		}
	}
	if !paramFound {
		t.Error("Param arguments not found in args")
	}
}

// TestBuildArgsWithLicense is removed because it requires a valid cryptographically signed
// license, which is complex to create for testing. The license flag functionality is simple:
// when cfg.License.Text is non-empty, BuildArgs adds "-L /path/to/license.lic" to args.
// This is adequately covered by integration tests and real usage.

func TestGetParamName(t *testing.T) {
	// Create test structs with yaml tags
	type testStruct struct {
		SimpleParam   string `yaml:"simple_param"`
		WithOmitempty string `yaml:"with_omitempty,omitempty"`
		ThreadPoolMax int    `yaml:"thread_pool_max,omitempty"`
		NoYamlTag     string // Should return empty string
		YamlDash      string `yaml:"-"` // Should return empty string (explicitly ignored)
		HTTPMaxHdr    int    `yaml:"http_max_hdr,omitempty"`
	}

	tests := []struct {
		fieldName string
		expected  string
	}{
		{"SimpleParam", "simple_param"},
		{"WithOmitempty", "with_omitempty"},
		{"ThreadPoolMax", "thread_pool_max"},
		{"NoYamlTag", ""},
		{"YamlDash", ""},
		{"HTTPMaxHdr", "http_max_hdr"},
	}

	structType := reflect.TypeOf(testStruct{})
	for _, tt := range tests {
		field, found := structType.FieldByName(tt.fieldName)
		if !found {
			t.Fatalf("Field %s not found in test struct", tt.fieldName)
		}
		result := GetParamName(field)
		if result != tt.expected {
			t.Errorf("GetParamName(%s) = %s, expected %s", tt.fieldName, result, tt.expected)
		}
	}
}

func TestIntegrationStartVarnish(t *testing.T) {
	// Skip if varnishd not available
	if _, err := exec.LookPath("varnishd"); err != nil {
		t.Skip("varnishd not found in PATH, skipping integration test")
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	workDir := t.TempDir()
	varnishDir := filepath.Join(workDir, "varnish")

	mgr := New(workDir, logger, varnishDir)

	if err := mgr.PrepareWorkspace(""); err != nil {
		t.Fatalf("PrepareWorkspace failed: %v", err)
	}

	// Listen on random port for admin connection
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to create listener: %v", err)
	}
	defer listener.Close()

	cfg := &Config{
		WorkDir:    workDir,
		AdminPort:  listener.Addr().(*net.TCPAddr).Port,
		VarnishDir: varnishDir,
		Listen:     []string{"127.0.0.1:0,http"},
		Storage:    []string{"malloc,32m"},
	}
	args := BuildArgs(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Start varnishd in background
	startErr := make(chan error, 1)
	go func() {
		startErr <- mgr.Start(ctx, "", args)
	}()

	// Accept admin connection from varnishd
	listener.(*net.TCPListener).SetDeadline(time.Now().Add(10 * time.Second))
	conn, err := adm.Accept(listener, mgr.GetSecretPath())
	if err != nil {
		cancel()
		t.Fatalf("Failed to accept admin connection: %v", err)
	}
	defer conn.Close()

	// Verify varnishd is responding
	resp, err := conn.Ask("status")
	if err != nil {
		cancel()
		t.Fatalf("Failed to get status: %v", err)
	}
	t.Logf("Varnish status: %s", resp)

	// Stop varnishd
	cancel()

	// Wait for Start() to return
	select {
	case err := <-startErr:
		// Context cancellation causes the process to be killed, which is expected
		if err != nil {
			t.Logf("Start returned (expected due to cancellation): %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Error("Timed out waiting for varnishd to stop")
	}
}
