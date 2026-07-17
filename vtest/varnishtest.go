// Package vtest provides facilities to build one-shot instances that you can test
// using regular golang HTTP entities.
// It's the "equivalent" of the [varnishtest] command
// but provides a more golang idiomatic interface.
//
// [varnishtest]: https://www.varnish-software.com/developers/tutorials/testing-varnish-varnishtest/
package vtest

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/varnish/varnish-go/adm"
	"github.com/varnish/varnish-go/stat"
	"github.com/varnish/varnish-go/version"
)

type parameter struct {
	name  string
	value string
}

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

// VarnishBuilder is a configuration object collecting options before the actual Varnish instance is started.
type VarnishBuilder struct {
	vclIsFile  bool
	vclString  string
	vclVersion string

	parameters  []parameter
	backends    []backend
	pemFiles    []pemFile
	sysLogChans []chan string

	noRecordLogs bool
	noSysLogs    bool
	tlsListener  bool

	licensePath string
	environ     []string

	buildErr error

	syslogs *syslogState
}

var envKeyRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// NoRecordLogs disables the background VSL record collector, making [Varnish.Records]
// always return an empty slice. [Varnish.RecordChannel] and
// [Varnish.TransactionChannel] will still work.
// This is useful to reduce resource usage for longer running tests which can use [Varnish.RecordChannel] instead.
func (vb *VarnishBuilder) NoRecordLogs() *VarnishBuilder {
	vb.noRecordLogs = true
	return vb
}

// NoSysLogs disables accumulation of stdout/stderr lines for [Varnish.SysLog].
// [VarnishBuilder.SysLogChannel] and [VarnishBuilder.SysLogChannel] continue to work.
// This is useful to reduce resource usage for longer running tests which can use channels instead.
func (vb *VarnishBuilder) NoSysLogs() *VarnishBuilder {
	vb.noSysLogs = true
	return vb
}

// SetLicensePath sets the path to the Varnish license file, passed to varnishd
// as the VARNISH_LICENSE environment variable.
func (vb *VarnishBuilder) SetLicensePath(path string) *VarnishBuilder {
	vb.licensePath = path
	return vb
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

// SetEnv sets an environment variable for the varnishd process. Setting the
// same key again replaces its previous value.
// If key does not follow POSIX environment variable name syntax
// ([A-Za-z_][A-Za-z0-9_]*), the error is recorded and returned by
// [VarnishBuilder.Start].
func (vb *VarnishBuilder) SetEnv(key, value string) *VarnishBuilder {
	if !envKeyRE.MatchString(key) {
		if vb.buildErr == nil {
			vb.buildErr = fmt.Errorf("SetEnv: invalid key %q: must match POSIX env name syntax ([A-Za-z_][A-Za-z0-9_]*)", key)
		}
		return vb
	}
	vb.environ = setEnvVar(vb.environ, key, value)
	return vb
}

// ClearEnv clears the environment variables for the varnishd process. The
// environment is inherited from the current process by default.
func (vb *VarnishBuilder) ClearEnv() *VarnishBuilder {
	vb.environ = []string{}
	return vb
}

// SysLogChannel returns a channel that will receive every stdout/stderr line
// emitted by the Varnish process, starting from startup. The channel is
// closed when the instance is stopped. Must be called before [VarnishBuilder.Start], but you can also use [Varnish.SysLogChannel] after the start.
func (vb *VarnishBuilder) SysLogChannel() <-chan string {
	ch := make(chan string, 64)
	vb.sysLogChans = append(vb.sysLogChans, ch)
	return ch
}

// SysLogs returns a snapshot of stdout/stderr lines collected during a failed
// [VarnishBuilder.Start]. Returns nil if [VarnishBuilder.Start] has not been called or succeeded.
func (vb *VarnishBuilder) SysLogs() []string {
	if vb.syslogs == nil {
		return nil
	}
	return vb.syslogs.snapshot()
}

// Varnish describes a running varnish instance, it must not be used once [Varnish.Stop] has been called.
type Varnish struct {
	// URL is the HTTP endpoint where Varnish is listening.
	// Varnish is started with a random port, discovered after startup.
	URL string

	// TLSURL is the HTTPS endpoint where Varnish is listening, if [VarnishBuilder.TLSListener] was called.
	TLSURL string

	cmd     *exec.Cmd
	name    string
	conn    *adm.Conn
	logs    *logState
	syslogs *syslogState
}

// New creates a new VarnishBuilder with default settings.
// It defaults to VCL version 4.1 and provides no backend by default.
func New() *VarnishBuilder {
	vb := &VarnishBuilder{
		environ: os.Environ(),
	}
	vb.Vcl41()
	return vb
}

// VclString provides a string containing the VCL to run.
// Note that the VCL version and backend definitions (according to [VarnishBuilder.Backend]) will be prepended to this string.
func (vb *VarnishBuilder) VclString(s string) *VarnishBuilder {
	vb.vclIsFile = false
	vb.vclString = s
	return vb
}

// VclFile selects a path to the VCL file to load.
func (vb *VarnishBuilder) VclFile(s string) *VarnishBuilder {
	vb.vclIsFile = true
	vb.vclString = s
	return vb
}

// Parameter appends a parameter to the varnishd command.
func (vb *VarnishBuilder) Parameter(name string, value string) *VarnishBuilder {
	vb.parameters = append(vb.parameters, parameter{name: name, value: value})
	return vb
}

// Vcl41 sets the VCL version to 4.1.
func (vb *VarnishBuilder) Vcl41() *VarnishBuilder {
	vb.vclVersion = "vcl 4.1;\n\n"
	return vb
}

// Vcl40 sets the VCL version to 4.0.
func (vb *VarnishBuilder) Vcl40() *VarnishBuilder {
	vb.vclVersion = "vcl 4.0;\n\n"
	return vb
}

// VCLVersion sets the VCL version to the value of version.
func (vb *VarnishBuilder) VCLVersion(version string) *VarnishBuilder {
	vb.vclVersion = version
	return vb
}

// tlsProto returns the protocol name for the TLS listener flag.
// Varnish Plus uses "https"; open-source Varnish uses "TLS".
func tlsProto() string {
	if version.IsEnterprise() {
		return "https"
	}
	return "TLS"
}

// TLSListener adds a TLS listener to the Varnish instance.
// After [VarnishBuilder.Start], the TLS endpoint is available via [Varnish.TLSURL].
// Use [VarnishBuilder.PEMFile] to load certificates automatically, or load them manually
// via [Varnish.Adm] using tls.cert.load + tls.cert.commit.
func (vb *VarnishBuilder) TLSListener() *VarnishBuilder {
	vb.tlsListener = true
	return vb
}

// PEMFile registers a TLS certificate to be loaded after Varnish starts, and implicitly enables [VarnishBuilder.TLSListener].
// certFile is the path to the PEM certificate file; keyFile is an optional separate private key file
// (pass "" if the key is embedded in certFile).
// Certificates are loaded via tls.cert.load and committed with tls.cert.commit at the end of [VarnishBuilder.Start].
func (vb *VarnishBuilder) PEMFile(certFile, keyFile string) *VarnishBuilder {
	vb.tlsListener = true
	vb.pemFiles = append(vb.pemFiles, pemFile{certFile: certFile, keyFile: keyFile})
	return vb
}

// Backend creates a VCL backend definition.
// Name must be a valid VCL backend name, otherwise Varnish will fail to start.
// This call will panic if urlRaw isn't parsable into a [url.URL].
func (vb *VarnishBuilder) Backend(name string, urlRaw string) *VarnishBuilder {
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

// Start starts a Varnish instance using the options specified in VarnishBuilder.
// The VarnishBuilder pointer must not be used after calling this function.
func (vb *VarnishBuilder) Start() (varnish Varnish, err error) {
	if vb.buildErr != nil {
		return varnish, vb.buildErr
	}

	sock, err := net.Listen("tcp", ":0")
	if err != nil {
		return
	}
	defer sock.Close()

	name := fmt.Sprintf("/tmp/varnishtest-go.%s", uuid.NewString())

	args := []string{
		"-F",
		"-f", "",
		"-n", name,
		"-a", "HTTP=127.0.0.1:0",
		"-p", "auto_restart=off",
		"-p", "syslog_cli_traffic=off",
		"-p", "thread_pool_min=10",
		"-p", "debug=+vtc_mode",
		"-p", "vsl_mask=+Debug,+H2RxHdr,+H2RxBody",
		"-p", "h2_initial_window_size=1m",
		"-p", "h2_rx_window_low_water=64k",
		"-M", sock.Addr().String(),
	}
	if vb.tlsListener {
		args = append(args, "-a", "HTTPS=127.0.0.1:0,"+tlsProto())
	}
	for _, p := range vb.parameters {
		args = append(args, p.name, p.value)
	}

	pr, pw := io.Pipe()

	cmd := exec.Command("varnishd", args...)
	cmd.Stdout = pw
	cmd.Stderr = pw
	cmd.Env = vb.environ
	switch {
	case vb.licensePath != "":
		cmd.Env = setEnvVar(cmd.Env, "VARNISH_LICENSE", vb.licensePath)
	case !hasEnvVar(vb.environ, "VARNISH_LICENSE"):
		cmd.Env = setEnvVar(cmd.Env, "VARNISH_LICENSE", "/usr/share/varnish-plus/vtc-license.dat")
	}

	err = cmd.Start()
	if err != nil {
		return
	}

	ss := newSyslogState(true, pw)
	ss.start(pr, cmd.Wait)
	vb.syslogs = ss

	var conn *adm.Conn
	{
		type acceptResult struct {
			conn *adm.Conn
			err  error
		}
		ch := make(chan acceptResult, 1)
		go func() {
			c, e := adm.Accept(context.Background(), sock, filepath.Join(name, "_.secret"))
			ch <- acceptResult{c, e}
		}()
		select {
		case res := <-ch:
			if res.err != nil {
				err = res.err
				return
			}
			conn = res.conn
		case <-ss.exited:
			sock.Close()
			err = fmt.Errorf("varnishd exited before connecting to management socket: check SysLogs for details")
			return
		}
	}

	varnish = Varnish{
		cmd:  cmd,
		name: name,
		conn: conn,
	}

	if vb.vclIsFile {
		_, err = varnish.Adm("vcl.load", "vcl1", vb.vclString)
		if err != nil {
			return
		}
	} else {
		var sb strings.Builder
		for _, b := range vb.backends {
			fmt.Fprintf(&sb, "backend %s {\n\t.host = %q;\n\t.port = %q;\n\t.host_header = %q;\n}\n",
				b.name, b.host, b.port, b.host)
		}
		backendString := sb.String()

		vcl := fmt.Sprintf("%s%s%s", vb.vclVersion, backendString, vb.vclString)
		_, err = varnish.Adm("vcl.inline", "vcl1 << XXYYZZ\n", vcl, "\nXXYYZZ")
		if err != nil {
			return
		}
	}

	_, err = varnish.Adm("vcl.use", "vcl1")
	if err != nil {
		return
	}
	_, err = varnish.Adm("start")
	if err != nil {
		return
	}

	err = varnish.WaitRunning()
	if err != nil {
		return
	}

	if len(vb.pemFiles) > 0 {
		for _, p := range vb.pemFiles {
			args := []string{"tls.cert.load", p.certFile}
			if p.keyFile != "" {
				args = append(args, "-k", p.keyFile)
			}
			if _, err = varnish.Adm(args...); err != nil {
				return
			}
		}
		if _, err = varnish.Adm("tls.cert.commit"); err != nil {
			return
		}
	}

	varnish.logs = newLogState()
	if !vb.noRecordLogs {
		varnish.logs.startCollector(name)
	}

	ss.transfer(!vb.noSysLogs, vb.sysLogChans)
	varnish.syslogs = ss
	vb.syslogs = nil

	return
}

// AssertStart calls [VarnishBuilder.Start] and calls t.Fatal if it fails.
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
func (vb *VarnishBuilder) AssertStart(t *testing.T) Varnish {
	t.Helper()
	v, err := vb.Start()
	if err != nil {
		t.Fatalf("vtest: Start: %v\n%s", err, strings.Join(vb.SysLogs(), "\n"))
	}
	return v
}

// Name returns the workdir path.
func (v *Varnish) Name() string {
	return v.name
}

// WaitRunning blocks until the Varnish child is running.
// You should generally not need this as it is already called as part of [VarnishBuilder.Start].
func (v *Varnish) WaitRunning() error {
	resp, err := v.Adm("status")
	for {
		if err != nil {
			return err
		}
		if resp == "Child in state stopped" {
			return fmt.Errorf("child stopped before running")
		}
		if resp == "Child in state running\n" {
			resp, err = v.Adm("debug.listen_address")
			if err != nil {
				return err
			}

			for _, line := range strings.Split(strings.TrimSpace(resp), "\n") {
				var lname, laddr string
				var lport int
				if _, scanErr := fmt.Sscanf(line, "%s %s %d", &lname, &laddr, &lport); scanErr != nil {
					continue
				}
				// FIXME: IPv6
				switch lname {
				case "HTTP":
					v.URL = fmt.Sprintf("http://%s:%d", laddr, lport)
				case "HTTPS":
					v.TLSURL = fmt.Sprintf("https://%s:%d", laddr, lport)
				}
			}
			if v.URL == "" && v.TLSURL == "" {
				return fmt.Errorf("could not determine listen address from: %s", resp)
			}
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	return nil
}

// AdmRaw sends a command to the admin socket, with more control and less convenience.
// It's just a passthrough for [adm.Conn.AskRaw].
func (v *Varnish) AdmRaw(args ...string) (int, []byte, error) {
	return v.conn.AskRaw(context.Background(), args...)
}

// Adm sends a command to the admin socket.
// It's just a passthrough for [adm.Conn.Ask].
func (v *Varnish) Adm(args ...string) (string, error) {
	return v.conn.Ask(context.Background(), args...)
}

// AdmConn returns the underlying admin connection.
func (v *Varnish) AdmConn() *adm.Conn {
	return v.conn
}

// CounterChecker is a fluent builder for polling a Varnish stat counter until a condition is met.
// Created via [Varnish.Counter]; configure with TryFor/TryEvery/MustExist, then call a terminal method.
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
	r, err := stat.New().SetName(c.v.name).Attach()
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

// Stop stops and cleans the running Varnish instance.
// The caller must call this to avoid littered file systems and forever-running processes.
func (v *Varnish) Stop() {
	if v.logs != nil {
		v.logs.stop()
	}
	_, _ = v.Adm("stop")
	_ = v.conn.Close()

	if err := v.cmd.Process.Kill(); err != nil {
		log.Printf("failed to kill process: %s\n", err)
	}

	if v.syslogs != nil {
		v.syslogs.stop()
	}

	_ = os.RemoveAll(v.name)
}
