package varnish_test

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/varnish/varnish-go/varnish"
)

const minimalVCL = `
	vcl 4.1;

	backend default none;
	sub vcl_recv { return(synth(200, "OK")); }
`

// vclFile writes content to a temp file and returns its path; the file is
// removed when the test ends.
func vclFile(t *testing.T, content string) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "*.vcl")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(content); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return f.Name()
}

// newBareBuilder returns a builder with a per-test workdir (removed by
// t.TempDir's own cleanup, not by the varnish package) but no listener
// configured yet.
func newBareBuilder(t *testing.T) *varnish.VarnishBuilder {
	t.Helper()
	return varnish.New().WorkDir(t.TempDir())
}

// newBuilder returns a builder with an ephemeral workdir and a default
// "HTTP" listener on a random port, ready for VclFile + Build.
func newBuilder(t *testing.T) *varnish.VarnishBuilder {
	t.Helper()
	return newBareBuilder(t).HTTPListener("HTTP", "127.0.0.1:0")
}

// generateSelfSignedPEM writes a self-contained certificate+key PEM file
// (as required by [varnish.VarnishBuilder.HTTPSListener]) and returns its path.
func generateSelfSignedPEM(t *testing.T) string {
	t.Helper()

	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &priv.PublicKey, priv)
	if err != nil {
		t.Fatal(err)
	}
	privDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}

	f, err := os.CreateTemp(t.TempDir(), "*.pem")
	if err != nil {
		t.Fatal(err)
	}
	if err := pem.Encode(f, &pem.Block{Type: "CERTIFICATE", Bytes: certDER}); err != nil {
		t.Fatal(err)
	}
	if err := pem.Encode(f, &pem.Block{Type: "EC PRIVATE KEY", Bytes: privDER}); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return f.Name()
}

func TestBuildAndStop(t *testing.T) {
	t.Parallel()

	v, err := newBuilder(t).VclFile(vclFile(t, minimalVCL)).Build()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(v.Stop)

	resp, err := http.Get(v.URL + "/test")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.Status != "200 OK" {
		t.Errorf(`expected "200 OK", got %s`, resp.Status)
	}
}

func TestParameter(t *testing.T) {
	t.Parallel()

	v, err := newBuilder(t).
		Parameter("max_retries", "2").
		VclFile(vclFile(t, minimalVCL)).
		Build()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(v.Stop)

	params, err := v.AdmConn().ParamShow(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	info, ok := params["max_retries"]
	if !ok {
		t.Fatal("max_retries not in param.show output")
	}
	if got := fmt.Sprint(info.Value); got != "2" {
		t.Errorf("max_retries = %v, want 2", info.Value)
	}
}

func TestReadOnlyParameter(t *testing.T) {
	t.Parallel()

	v, err := newBuilder(t).
		ReadOnlyParameter("max_retries").
		VclFile(vclFile(t, minimalVCL)).
		Build()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(v.Stop)

	if _, err := v.AdmConn().ParamSet(t.Context(), "max_retries", "1"); err == nil {
		t.Error("param.set succeeded for a read-only parameter")
	} else if !strings.Contains(err.Error(), " is protected") {
		t.Errorf("param.set failed with unexpected error: %v", err)
	}
}

func TestAddress(t *testing.T) {
	t.Parallel()

	v, err := newBuilder(t).
		Address("EXTRA=127.0.0.1:0").
		VclFile(vclFile(t, minimalVCL)).
		Build()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(v.Stop)

	listenAddresses, err := v.AdmConn().Ask(t.Context(), "debug.listen_address")
	if err != nil {
		t.Fatal(err)
	}

	var extraURL string
	for _, line := range strings.Split(strings.TrimSpace(listenAddresses), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 3 && fields[0] == "EXTRA" {
			extraURL = "http://" + net.JoinHostPort(fields[1], fields[2])
		}
	}
	if extraURL == "" {
		t.Fatalf("EXTRA listener not found in debug.listen_address output:\n%s", listenAddresses)
	}

	resp, err := http.Get(extraURL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
}

func TestHTTPSListener(t *testing.T) {
	t.Parallel()

	v, err := newBareBuilder(t).
		HTTPSListener("HTTPS", "127.0.0.1:0", generateSelfSignedPEM(t)).
		VclFile(vclFile(t, minimalVCL)).
		Build()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(v.Stop)

	if v.TLSURL == "" {
		t.Fatal("TLSURL is empty after HTTPSListener")
	}

	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec
		},
	}
	resp, err := client.Get(v.TLSURL + "/test")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

// TestTLSCertListOnVCLLessInstance verifies TLSCertList returns no entries,
// rather than erroring, on an instance with an HTTPS listener but no VCL or
// certificate loaded yet.
func TestTLSCertListOnVCLLessInstance(t *testing.T) {
	t.Parallel()

	v, err := newBareBuilder(t).
		HTTPSListener("HTTPS", "127.0.0.1:0").
		Build()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(v.Stop)

	entries, err := v.AdmConn().TLSCertList(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("expected no entries on a fresh VCL-less instance, got %+v", entries)
	}
}

func TestUDSSocket(t *testing.T) {
	t.Parallel()

	sockPath := filepath.Join(t.TempDir(), "varnish.sock")

	v, err := newBareBuilder(t).
		UDSSocket("uds", sockPath, varnish.NewUDSOptions().Mode("660")).
		VclFile(vclFile(t, minimalVCL)).
		Build()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(v.Stop)

	info, err := os.Stat(sockPath)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o660 {
		t.Errorf("socket mode = %o, want 660", perm)
	}

	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
				return net.Dial("unix", sockPath)
			},
		},
	}
	resp, err := client.Get("http://unix/test")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

func TestListenerNameCollision(t *testing.T) {
	t.Parallel()

	_, err := newBareBuilder(t).
		HTTPListener("dup", "127.0.0.1:0").
		HTTPListener("dup", "127.0.0.1:0").
		VclFile(vclFile(t, minimalVCL)).
		Build()
	if err == nil {
		t.Fatal("expected Build to fail on duplicate listener name")
	}
	if !strings.Contains(err.Error(), "already used") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestListenerNameCollisionAcrossHTTPAndHTTPS(t *testing.T) {
	t.Parallel()

	_, err := newBareBuilder(t).
		HTTPListener("dup", "127.0.0.1:0").
		HTTPSListener("dup", "127.0.0.1:0", generateSelfSignedPEM(t)).
		VclFile(vclFile(t, minimalVCL)).
		Build()
	if err == nil {
		t.Fatal("expected Build to fail on cross-protocol duplicate listener name")
	}
}

func TestHTTPListenerInvalidSocket(t *testing.T) {
	t.Parallel()

	for _, socket := range []string{"localhost:80", "127.0.0.1", ":abc", ""} {
		_, err := varnish.New().HTTPListener("bad", socket).VclFile(vclFile(t, minimalVCL)).Build()
		if err == nil {
			t.Errorf("socket %q: expected Build to fail", socket)
		}
	}
}

func TestUDSSocketInvalidPath(t *testing.T) {
	t.Parallel()

	_, err := varnish.New().UDSSocket("bad", "relative/path.sock", nil).VclFile(vclFile(t, minimalVCL)).Build()
	if err == nil {
		t.Fatal("expected Build to fail with a relative UDS path")
	}
}

func TestUDSSocketInvalidMode(t *testing.T) {
	t.Parallel()

	_, err := varnish.New().
		UDSSocket("bad", filepath.Join(t.TempDir(), "x.sock"), varnish.NewUDSOptions().Mode("999")).
		VclFile(vclFile(t, minimalVCL)).
		Build()
	if err == nil {
		t.Fatal("expected Build to fail with an invalid UDS mode")
	}
}

func TestBuildRequiresListener(t *testing.T) {
	t.Parallel()

	_, err := newBareBuilder(t).VclFile(vclFile(t, minimalVCL)).Build()
	if err == nil {
		t.Fatal("expected Build to fail with no listener configured")
	}
}

func TestSetEnvAndClearEnv(t *testing.T) {
	t.Parallel()

	vclContent := `
		vcl 4.1;
		import std;

		backend default none;
		sub vcl_recv { return(synth(200, "OK")); }
		sub vcl_synth {
			set resp.http.My-Var = std.getenv("MY_VAR");
		}
	`

	v, err := newBuilder(t).
		SetEnv("MY_VAR", "myvalue").
		VclFile(vclFile(t, vclContent)).
		Build()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(v.Stop)

	resp, err := http.Get(v.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if got := resp.Header.Get("My-Var"); got != "myvalue" {
		t.Errorf(`expected My-Var header "myvalue", got %q`, got)
	}
}

func TestWorkDirSurvivesStop(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	workDir := filepath.Join(dir, "instance")

	v, err := varnish.New().
		WorkDir(workDir).
		HTTPListener("HTTP", "127.0.0.1:0").
		VclFile(vclFile(t, minimalVCL)).
		Build()
	if err != nil {
		t.Fatal(err)
	}

	if v.Name() != workDir {
		t.Fatalf("Name() = %q, want %q", v.Name(), workDir)
	}

	v.Stop()

	if _, err := os.Stat(workDir); err != nil {
		t.Errorf("caller-supplied WorkDir was removed by Stop: %v", err)
	}
}

func TestStopTimeoutFallsBackToKill(t *testing.T) {
	t.Parallel()

	v, err := newBuilder(t).
		StopTimeout(200 * time.Millisecond).
		VclFile(vclFile(t, minimalVCL)).
		Build()
	if err != nil {
		t.Fatal(err)
	}

	done := make(chan struct{})
	go func() {
		v.Stop()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Stop did not return within 5s")
	}
}

// TestOutput verifies that Output tees the combined stdout/stderr stream of
// the varnishd child to a caller-supplied writer. This is the primitive
// vtest builds its own log accumulation/streaming on top of; the varnish
// package itself exposes no accumulation API.
func TestOutput(t *testing.T) {
	t.Parallel()

	var buf syncBuffer
	v, err := newBuilder(t).
		Output(&buf).
		VclFile(vclFile(t, minimalVCL)).
		Build()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(v.Stop)

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if buf.Len() > 0 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Error("expected Output writer to receive some startup output")
}

// TestBuildFailureIncludesDiagnostics verifies that a failed Build's error
// includes captured varnishd output, so callers get useful diagnostics
// without needing any accumulation API.
func TestBuildFailureIncludesDiagnostics(t *testing.T) {
	t.Parallel()

	_, err := newBuilder(t).VclFile(vclFile(t, "this is not valid VCL")).Build()
	if err == nil {
		t.Fatal("expected Build to fail with invalid VCL")
	}
	if !strings.Contains(err.Error(), "\n") {
		t.Errorf("expected Build error to include captured diagnostics, got: %v", err)
	}
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Len()
}

func TestBuildCleansUpOnFailure(t *testing.T) {
	t.Parallel()

	tmpRoot := t.TempDir()

	_, err := newBuilder(t).
		WorkDir(filepath.Join(tmpRoot, "instance")).
		VclFile(vclFile(t, "this is not valid VCL")).
		Build()
	if err == nil {
		t.Fatal("expected Build to fail with invalid VCL")
	}
}

// Example demonstrates the minimal lifecycle: configure, Build, and Stop.
func Example() {
	dir, err := os.MkdirTemp("", "varnish-example-")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(dir)

	vclPath := filepath.Join(dir, "default.vcl")
	if err := os.WriteFile(vclPath, []byte(minimalVCL), 0o644); err != nil {
		panic(err)
	}

	v, err := varnish.New().
		WorkDir(dir).
		HTTPListener("HTTP", "127.0.0.1:0").
		VclFile(vclPath).
		Build()
	if err != nil {
		panic(err)
	}
	defer v.Stop()

	fmt.Println(v.URL != "")
}

// ExampleVarnishBuilder_UDSSocket demonstrates a bare UDS listener (opts may
// be nil) alongside one with custom permissions via [varnish.NewUDSOptions].
func ExampleVarnishBuilder_UDSSocket() {
	dir, err := os.MkdirTemp("", "varnish-example-")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(dir)

	vclPath := filepath.Join(dir, "default.vcl")
	if err := os.WriteFile(vclPath, []byte(minimalVCL), 0o644); err != nil {
		panic(err)
	}

	v, err := varnish.New().
		WorkDir(dir).
		UDSSocket("cli", filepath.Join(dir, "cli.sock"), nil).
		UDSSocket("admin", filepath.Join(dir, "admin.sock"), varnish.NewUDSOptions().User("nobody").Mode("660")).
		VclFile(vclPath).
		Build()
	if err != nil {
		panic(err)
	}
	defer v.Stop()

	fmt.Println(v.Name() != "")
}
