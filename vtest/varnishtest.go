// Package vtest provides facilities to build one-shot instances that you can test
// using regular golang HTTP entities.
// It's the "equivalent" of the [varnishtest] command
// but provides a more golang idiomatic interface.
//
// It wraps the [varnish] package, restoring test-oriented defaults (inline
// VCL strings, injected backends, always-on log accumulation, a fallback
// license path) on top of its generic varnishd process management.
//
// [varnishtest]: https://www.varnish-software.com/developers/tutorials/testing-varnish-varnishtest/
package vtest

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/varnish/varnish-go/varnish"
	"github.com/varnish/varnish-go/version"
)

const defaultLicensePath = "/usr/share/varnish-plus/vtc-license.dat"

type backend struct {
	name string
	host string
	port string
	tls  bool
}

type pemFile struct {
	certFile string
	keyFile  string // empty = key embedded in certFile
}

// VarnishTestBuilder is a configuration object collecting options before the actual Varnish instance is started.
// It wraps [varnish.VarnishBuilder], adding test-only conveniences.
type VarnishTestBuilder struct {
	*varnish.VarnishBuilder

	vclIsFile   bool
	vclFilePath string
	vclString   string
	vclVersion  string

	backends []backend

	tlsListener bool
	pemFiles    []pemFile

	noRecordLogs bool
	noSysLogs    bool
	sysLogChans  []chan string
	syslogs      *syslogState

	environ        []string
	licensePathSet bool
}

// setEnvVar sets key to value in environ, replacing any existing entry for
// key in place, or appending a new one.
func setEnvVar(environ []string, key, value string) []string {
	prefix := key + "="
	for i, kv := range environ {
		if strings.HasPrefix(kv, prefix) {
			environ[i] = prefix + value
			return environ
		}
	}
	return append(environ, prefix+value)
}

// hasEnvVar reports whether key is already set in environ.
func hasEnvVar(environ []string, key string) bool {
	prefix := key + "="
	for _, kv := range environ {
		if strings.HasPrefix(kv, prefix) {
			return true
		}
	}
	return false
}

// New creates a new VarnishTestBuilder with default settings.
// It defaults to VCL version 4.1, provides no backend, and (unlike
// [varnish.New]) accumulates VSL records and syslog lines for
// [Varnish.Records] / [Varnish.SysLogs].
func New() *VarnishTestBuilder {
	vtb := &VarnishTestBuilder{
		VarnishBuilder: varnish.New(),
		environ:        os.Environ(),
	}
	// varnishtest-equivalent tuning: fast, quiet, debug-friendly startup,
	// zero-config default HTTP listener. The isolated per-instance workdir
	// is set up in Start, which owns its full lifecycle (creation +
	// removal on Stop or failure). These are vtest-only defaults, not part
	// of the generic varnish package.
	vtb.VarnishBuilder.
		HTTPListener("HTTP", "127.0.0.1:0").
		Parameter("auto_restart", "off").
		Parameter("syslog_cli_traffic", "off").
		Parameter("thread_pool_min", "10").
		Parameter("debug", "+vtc_mode").
		Parameter("vsl_mask", "+Debug,+H2RxHdr,+H2RxBody").
		Parameter("h2_initial_window_size", "1m").
		Parameter("h2_rx_window_low_water", "64k")
	vtb.Vcl41()
	return vtb
}

// VclString provides a string containing the VCL to run.
// Note that the VCL version and backend definitions (according to [VarnishTestBuilder.Backend]) will be prepended to this string.
func (vb *VarnishTestBuilder) VclString(s string) *VarnishTestBuilder {
	vb.vclIsFile = false
	vb.vclString = s
	return vb
}

// VclFile selects a path to the VCL file to load, bypassing VCL version and
// backend injection: the file must be self-contained.
func (vb *VarnishTestBuilder) VclFile(path string) *VarnishTestBuilder {
	vb.vclIsFile = true
	vb.vclFilePath = path
	return vb
}

// Vcl41 sets the VCL version to 4.1.
func (vb *VarnishTestBuilder) Vcl41() *VarnishTestBuilder {
	vb.vclVersion = "vcl 4.1;\n\n"
	return vb
}

// Vcl40 sets the VCL version to 4.0.
func (vb *VarnishTestBuilder) Vcl40() *VarnishTestBuilder {
	vb.vclVersion = "vcl 4.0;\n\n"
	return vb
}

// VCLVersion sets the VCL version to the value of version.
func (vb *VarnishTestBuilder) VCLVersion(version string) *VarnishTestBuilder {
	vb.vclVersion = version
	return vb
}

// Backend creates a VCL backend definition.
// Name must be a valid VCL backend name, otherwise Varnish will fail to start.
// This call will panic if urlRaw isn't parsable into a [url.URL].
func (vb *VarnishTestBuilder) Backend(name string, urlRaw string) *VarnishTestBuilder {
	u, err := url.Parse(urlRaw)
	if err != nil {
		panic(err)
	}

	tls := false
	port := u.Port()

	if u.Scheme == "https" {
		tls = true
		if port == "" {
			port = "443"
		}
	} else if port == "" {
		port = "80"
	}

	host := u.Hostname()

	vb.backends = append(vb.backends, backend{
		name: name,
		host: host,
		port: port,
		tls:  tls,
	})
	return vb
}

// backendDefinitions renders the VCL backend blocks for the backends added
// with [VarnishTestBuilder.Backend]. Backends with an https URL get a TLS
// backend on Varnish Enterprise; Varnish Cache has no native backend TLS,
// so they produce an error instead.
func (vb *VarnishTestBuilder) backendDefinitions() (string, error) {
	var sb strings.Builder
	for _, b := range vb.backends {
		if b.tls && !version.IsEnterprise() {
			return "", fmt.Errorf("vtest: backend %q uses an https URL, but backend TLS requires Varnish Enterprise", b.name)
		}
		fmt.Fprintf(&sb, "backend %s {\n\t.host = %q;\n\t.port = %q;\n\t.host_header = %q;\n",
			b.name, b.host, b.port, b.host)
		if b.tls {
			// Certificate verification stays off: vtest backends are
			// typically local httptest servers with self-signed
			// certificates.
			sb.WriteString("\t.ssl = 1;\n\t.ssl_verify_peer = 0;\n\t.ssl_verify_host = 0;\n")
		}
		sb.WriteString("}\n")
	}
	return sb.String(), nil
}

// combinePEMFiles resolves each [VarnishTestBuilder.PEMFile] entry into a
// self-contained cert+key path, as required by
// [varnish.VarnishBuilder.HTTPSListener]: entries with a separate keyFile are
// combined into a new file under dir; entries without one are used as-is.
func (vb *VarnishTestBuilder) combinePEMFiles(dir string) ([]string, error) {
	paths := make([]string, 0, len(vb.pemFiles))
	for i, p := range vb.pemFiles {
		if p.keyFile == "" {
			paths = append(paths, p.certFile)
			continue
		}
		cert, err := os.ReadFile(p.certFile)
		if err != nil {
			return nil, err
		}
		key, err := os.ReadFile(p.keyFile)
		if err != nil {
			return nil, err
		}
		combined := filepath.Join(dir, fmt.Sprintf("pemfile-%d.pem", i))
		if err := os.WriteFile(combined, append(append(cert, '\n'), key...), 0o600); err != nil {
			return nil, err
		}
		paths = append(paths, combined)
	}
	return paths, nil
}

// ReadOnlyParameter marks a parameter as read-only. The parameter cannot be changed at runtime via varnishadm.
func (vb *VarnishTestBuilder) ReadOnlyParameter(names ...string) *VarnishTestBuilder {
	vb.VarnishBuilder.ReadOnlyParameter(names...)
	return vb
}

// Address adds a listener address to the Varnish instance.
func (vb *VarnishTestBuilder) Address(addresses ...string) *VarnishTestBuilder {
	vb.VarnishBuilder.Address(addresses...)
	return vb
}

// Jail sets the jail mechanism to use.
func (vb *VarnishTestBuilder) Jail(jail string) *VarnishTestBuilder {
	vb.VarnishBuilder.Jail(jail)
	return vb
}

// Parameter appends a -p name=value startup parameter to the varnishd
// command line. Parameters that are runtime-settable can also be changed
// after start via [Varnish.AdmConn] and [adm.Conn.ParamSet].
func (vb *VarnishTestBuilder) Parameter(name string, value string) *VarnishTestBuilder {
	vb.VarnishBuilder.Parameter(name, value)
	return vb
}

// HTTPListener adds a named plain-HTTP listener. See [varnish.VarnishBuilder.HTTPListener].
func (vb *VarnishTestBuilder) HTTPListener(name, socket string) *VarnishTestBuilder {
	vb.VarnishBuilder.HTTPListener(name, socket)
	return vb
}

// HTTPSListener adds a named HTTPS listener, with automatic certificate
// loading. See [varnish.VarnishBuilder.HTTPSListener].
func (vb *VarnishTestBuilder) HTTPSListener(name, socket string, pemFiles ...string) *VarnishTestBuilder {
	vb.VarnishBuilder.HTTPSListener(name, socket, pemFiles...)
	return vb
}

// UDSSocket adds a named Unix domain socket listener. See [varnish.VarnishBuilder.UDSSocket].
func (vb *VarnishTestBuilder) UDSSocket(name, path string, opts *varnish.UDSOptions) *VarnishTestBuilder {
	vb.VarnishBuilder.UDSSocket(name, path, opts)
	return vb
}

// TLSListener adds an HTTPS listener named "HTTPS" on a random port.
// After [VarnishTestBuilder.Start], the TLS endpoint is available via [Varnish.TLSURL].
// Use [VarnishTestBuilder.PEMFile] to load certificates automatically, or load
// them manually via [Varnish.Adm] using tls.cert.load + tls.cert.commit.
func (vb *VarnishTestBuilder) TLSListener() *VarnishTestBuilder {
	vb.tlsListener = true
	return vb
}

// PEMFile registers a TLS certificate to be loaded after Varnish starts, and
// implicitly enables [VarnishTestBuilder.TLSListener]. certFile is the path to
// the PEM certificate file; keyFile is an optional separate private key file
// (pass "" if the key is embedded in certFile).
func (vb *VarnishTestBuilder) PEMFile(certFile, keyFile string) *VarnishTestBuilder {
	vb.tlsListener = true
	vb.pemFiles = append(vb.pemFiles, pemFile{certFile: certFile, keyFile: keyFile})
	return vb
}

// NoRecordLogs disables the background VSL record collector, making [Varnish.Records]
// always return an empty slice. [Varnish.RecordChannel] and
// [Varnish.TransactionChannel] will still work.
// This is useful to reduce resource usage for longer running tests which can use [Varnish.RecordChannel] instead.
func (vb *VarnishTestBuilder) NoRecordLogs() *VarnishTestBuilder {
	vb.noRecordLogs = true
	return vb
}

// NoSysLogs disables accumulation of stdout/stderr lines for [Varnish.SysLogs].
// [VarnishTestBuilder.SysLogChannel] and [Varnish.SysLogChannel] continue to work.
// This is useful to reduce resource usage for longer running tests which can use channels instead.
func (vb *VarnishTestBuilder) NoSysLogs() *VarnishTestBuilder {
	vb.noSysLogs = true
	return vb
}

// SetLicensePath sets the path to the Varnish license file, passed to varnishd
// as the VARNISH_LICENSE environment variable.
func (vb *VarnishTestBuilder) SetLicensePath(path string) *VarnishTestBuilder {
	vb.licensePathSet = true
	vb.VarnishBuilder.SetLicensePath(path)
	return vb
}

// SetEnv sets an environment variable for the varnishd process. Setting the
// same key again replaces its previous value.
// If key does not follow POSIX environment variable name syntax
// ([A-Za-z_][A-Za-z0-9_]*), the error is recorded and returned by
// [VarnishTestBuilder.Start].
func (vb *VarnishTestBuilder) SetEnv(key, value string) *VarnishTestBuilder {
	vb.environ = setEnvVar(vb.environ, key, value)
	vb.VarnishBuilder.SetEnv(key, value)
	return vb
}

// ClearEnv clears the environment variables for the varnishd process. The
// environment is inherited from the current process by default.
func (vb *VarnishTestBuilder) ClearEnv() *VarnishTestBuilder {
	vb.environ = []string{}
	vb.VarnishBuilder.ClearEnv()
	return vb
}

// SysLogChannel returns a channel that will receive every stdout/stderr line
// emitted by the Varnish process, starting from startup. The channel is
// closed when the instance is stopped. Must be called before [VarnishTestBuilder.Start], but you can also use [Varnish.SysLogChannel] after the start.
func (vb *VarnishTestBuilder) SysLogChannel() <-chan string {
	ch := make(chan string, 64)
	vb.sysLogChans = append(vb.sysLogChans, ch)
	return ch
}

// SysLogs returns a snapshot of stdout/stderr lines collected during a failed
// [VarnishTestBuilder.Start]. Returns nil if [VarnishTestBuilder.Start] has not been called or succeeded.
func (vb *VarnishTestBuilder) SysLogs() []string {
	if vb.syslogs == nil {
		return nil
	}
	return vb.syslogs.snapshot()
}

// Varnish describes a running varnish instance, it must not be used once [Varnish.Stop] has been called.
type Varnish struct {
	varnish.Varnish
	logs    *logState
	syslogs *syslogState
}

// Stop stops the running instance, releases vtest's own VSL/syslog
// accumulation resources, and removes its per-instance workdir (unlike the
// underlying [varnish.Varnish.Stop], which never removes it).
func (v *Varnish) Stop() {
	if v.logs != nil {
		v.logs.stop()
	}
	name := v.Name()
	v.Varnish.Stop()
	if v.syslogs != nil {
		v.syslogs.stop()
	}
	_ = os.RemoveAll(name)
}

// AdmRaw sends a command to the admin socket, with more control and less
// convenience. It's just a passthrough for [adm.Conn.AskRaw].
func (v *Varnish) AdmRaw(args ...string) (int, []byte, error) {
	return v.AdmConn().AskRaw(context.Background(), args...)
}

// Adm sends a command to the admin socket.
// It's just a passthrough for [adm.Conn.Ask].
func (v *Varnish) Adm(args ...string) (string, error) {
	return v.AdmConn().Ask(context.Background(), args...)
}

// Start starts a Varnish instance using the options specified in VarnishTestBuilder.
// The VarnishTestBuilder pointer must not be used after calling this function.
func (vb *VarnishTestBuilder) Start() (Varnish, error) {
	if !vb.licensePathSet && !hasEnvVar(vb.environ, "VARNISH_LICENSE") {
		vb.VarnishBuilder.SetLicensePath(defaultLicensePath)
	}

	// vtest owns the full lifecycle of this workdir (unlike the varnish
	// package's default, fixed, never-removed one): created here, removed
	// on any failure below or by Varnish.Stop on success.
	workDir, err := os.MkdirTemp("", "varnishtest-go.")
	if err != nil {
		return Varnish{}, err
	}
	vb.VarnishBuilder.WorkDir(workDir)

	// Always collect during startup, regardless of NoSysLogs, so a failed
	// Start still has diagnostics available via SysLogs; transfer() below
	// applies the final collection mode once Build has succeeded.
	pr, pw := io.Pipe()
	ss := newSyslogState(true, pw)
	ss.start(pr)
	vb.syslogs = ss
	vb.VarnishBuilder.Output(pw)

	startFailed := true
	defer func() {
		if startFailed {
			pw.Close()
			ss.wg.Wait()
			_ = os.RemoveAll(workDir)
		}
	}()

	if vb.tlsListener {
		pemPaths, err := vb.combinePEMFiles(workDir)
		if err != nil {
			return Varnish{}, err
		}
		vb.VarnishBuilder.HTTPSListener("HTTPS", "127.0.0.1:0", pemPaths...)
	}

	if vb.vclIsFile {
		vb.VarnishBuilder.VclFile(vb.vclFilePath)
	} else {
		backendString, err := vb.backendDefinitions()
		if err != nil {
			return Varnish{}, err
		}
		vclContent := vb.vclVersion + backendString + vb.vclString
		vb.VarnishBuilder.VclString(vclContent)
	}

	v, err := vb.VarnishBuilder.Build()
	if err != nil {
		return Varnish{}, err
	}
	ss.transfer(!vb.noSysLogs, vb.sysLogChans)
	varnish := Varnish{Varnish: v, syslogs: ss}
	vb.syslogs = nil

	varnish.logs = newLogState()
	if !vb.noRecordLogs {
		varnish.logs.startCollector(&varnish)
	}

	startFailed = false
	return varnish, nil
}

// AssertStart calls [VarnishTestBuilder.Start] and calls t.Fatal if it fails.
// SysLogs output is included in the error message to aid debugging.
//
// To verify that bad VCL causes a failure:
//
//	// t is a *testing.T passed to the test function
//	varnish := vtest.New().VclString(`
//		backend default none;
//		sub vcl_recv {
//			return(invalid_action);
//		}
//	`).AssertStart(t)
//	defer varnish.Stop()
func (vb *VarnishTestBuilder) AssertStart(t *testing.T) Varnish {
	t.Helper()
	v, err := vb.Start()
	if err != nil {
		t.Fatalf("vtest: Start: %v\n%s", err, strings.Join(vb.SysLogs(), "\n"))
	}
	return v
}

// CounterChecker is a fluent builder for polling a Varnish stat counter until a condition is met.
// Created via [Varnish.Counter], configure with TryFor/TryEvery/MustExist, then call a terminal method.
type CounterChecker struct {
	v         *Varnish
	name      string
	tryFor    time.Duration
	tryEvery  time.Duration
	mustExist bool
}

// Counter returns a CounterChecker for the named counter (e.g. "MAIN.cache_hit").
func (v *Varnish) Counter(name string) *CounterChecker {
	return &CounterChecker{
		v:        v,
		name:     name,
		tryFor:   5 * time.Second,
		tryEvery: 100 * time.Millisecond,
	}
}

// TryFor sets the maximum duration to retry before failing. Default: 5s.
func (c *CounterChecker) TryFor(d time.Duration) *CounterChecker {
	c.tryFor = d
	return c
}

// TryEvery sets the polling interval. Default: 100ms.
func (c *CounterChecker) TryEvery(d time.Duration) *CounterChecker {
	c.tryEvery = d
	return c
}

// MustExist causes any terminal check to fail immediately if the counter is not found,
// rather than retrying until TryFor expires.
func (c *CounterChecker) MustExist() *CounterChecker {
	c.mustExist = true
	return c
}

func (c *CounterChecker) fetch() (uint64, bool, error) {
	r, err := c.v.StatReaderBuilder().Attach()
	if err != nil {
		return 0, false, err
	}
	defer r.Close()
	if _, _, err := r.Update(); err != nil {
		return 0, false, err
	}
	s, ok := r.Stats[c.name]
	if !ok {
		return 0, false, nil
	}
	return *s.Value, true, nil
}

func (c *CounterChecker) wait(f func(uint64) bool) error {
	deadline := time.Now().Add(c.tryFor)
	var lastVal uint64
	everFound := false
	for time.Now().Before(deadline) {
		val, found, err := c.fetch()
		if err != nil {
			return err
		}
		if !found {
			if c.mustExist {
				return fmt.Errorf("counter %q not found", c.name)
			}
			time.Sleep(c.tryEvery)
			continue
		}
		everFound = true
		lastVal = val
		if f(val) {
			return nil
		}
		time.Sleep(c.tryEvery)
	}
	if !everFound {
		return fmt.Errorf("counter %q not found after %s", c.name, c.tryFor)
	}
	return fmt.Errorf("counter %q = %d did not satisfy condition after %s", c.name, lastVal, c.tryFor)
}

// Value waits for the counter to appear and returns its current value.
func (c *CounterChecker) Value() (uint64, error) {
	deadline := time.Now().Add(c.tryFor)
	for time.Now().Before(deadline) {
		val, found, err := c.fetch()
		if err != nil {
			return 0, err
		}
		if !found {
			if c.mustExist {
				return 0, fmt.Errorf("counter %q not found", c.name)
			}
			time.Sleep(c.tryEvery)
			continue
		}
		return val, nil
	}
	return 0, fmt.Errorf("counter %q not found after %s", c.name, c.tryFor)
}

// Equals waits until the counter value equals n.
func (c *CounterChecker) Equals(n uint64) error {
	return c.wait(func(v uint64) bool { return v == n })
}

// AssertEquals calls [CounterChecker.Equals] and calls t.Fatal if it fails.
func (c *CounterChecker) AssertEquals(t *testing.T, n uint64) {
	t.Helper()
	if err := c.Equals(n); err != nil {
		t.Fatal(err)
	}
}

// NotEquals waits until the counter value does not equal n.
func (c *CounterChecker) NotEquals(n uint64) error {
	return c.wait(func(v uint64) bool { return v != n })
}

// AtLeast waits until the counter value is >= n.
func (c *CounterChecker) AtLeast(n uint64) error {
	return c.wait(func(v uint64) bool { return v >= n })
}

// AtMost waits until the counter value is <= n.
func (c *CounterChecker) AtMost(n uint64) error {
	return c.wait(func(v uint64) bool { return v <= n })
}

// GreaterThan waits until the counter value is > n.
func (c *CounterChecker) GreaterThan(n uint64) error {
	return c.wait(func(v uint64) bool { return v > n })
}

// LessThan waits until the counter value is < n.
func (c *CounterChecker) LessThan(n uint64) error {
	return c.wait(func(v uint64) bool { return v < n })
}

// WithTestFunction waits until f returns true for the counter value.
func (c *CounterChecker) WithTestFunction(f func(uint64) bool) error {
	return c.wait(f)
}
