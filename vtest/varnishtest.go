// Package vtest provides facilities to build one-shot instances that you can test
// using regular golang HTTP entities.
// It's the "equivalent" of the [varnishtest] command
// but provides a more golang idiomatic interface.
//
// [varnishtest]: https://www.varnish-software.com/developers/tutorials/testing-varnish-varnishtest/
package vtest

import (
	"fmt"
	"io"
	"log"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/varnish/varnish-go/adm"
	"github.com/varnish/varnish-go/stat"
)

type parameter struct {
	name  string
	value string
}

type backend struct {
	name string
	host string
	port string
	ssl  bool
}

// VarnishBuilder is a configuration object collecting options before the actual Varnish instance is started.
type VarnishBuilder struct {
	vclIsFile  bool
	vclString  string
	vclVersion string

	parameters  []parameter
	backends    []backend
	sysLogChans []chan string

	noRecordLogs bool
	noSysLogs    bool

	licensePath string

	syslogs *syslogState
}

// NoRecordLogs disables the background VSL record collector, making [Varnish.Records]
// always return an empty slice. [Varnish.RecordChannel] and
// [Varnish.TransactionChannel] will still work.
func (vb *VarnishBuilder) NoRecordLogs() *VarnishBuilder {
	vb.noRecordLogs = true
	return vb
}

// NoSysLogs disables accumulation of stdout/stderr lines for [Varnish.SysLog].
// [VarnishBuilder.SysLogChannel] and [Varnish.SysLogChannel] continue to work.
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

// SysLogChannel returns a channel that will receive every stdout/stderr line
// emitted by the Varnish process, starting from startup. The channel is
// closed when the instance is stopped. Must be called before [VarnishBuilder.Start].
func (vb *VarnishBuilder) SysLogChannel() <-chan string {
	ch := make(chan string, 64)
	vb.sysLogChans = append(vb.sysLogChans, ch)
	return ch
}

// SysLogs returns a snapshot of stdout/stderr lines collected during a failed
// [VarnishBuilder.Start]. Returns nil if Start has not been called or succeeded.
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

	cmd     *exec.Cmd
	name    string
	conn    adm.Conn
	logs    *logState
	syslogs *syslogState
}

// New creates a new VarnishBuilder with default settings.
// It defaults to VCL version 4.1 and provides no backend by default.
func New() *VarnishBuilder {
	vb := &VarnishBuilder{}
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

// Backend creates a VCL backend definition.
// Name must be a valid VCL backend name, otherwise Varnish will fail to start.
// This call will panic if urlRaw isn't parsable into a [url.URL].
func (vb *VarnishBuilder) Backend(name string, urlRaw string) *VarnishBuilder {
	u, err := url.Parse(urlRaw)
	if err != nil {
		panic(err)
	}

	ssl := false
	port := u.Port()

	if u.Scheme == "https" {
		ssl = true
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
		ssl:  ssl,
	})
	return vb
}

// Start starts a Varnish instance using the options specified in VarnishBuilder.
// The VarnishBuilder pointer must not be used after calling this function.
func (vb *VarnishBuilder) Start() (varnish Varnish, err error) {
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
		"-a", "127.0.0.1:0",
		"-p", "auto_restart=off",
		"-p", "syslog_cli_traffic=off",
		"-p", "thread_pool_min=10",
		"-p", "debug=+vtc_mode",
		"-p", "vsl_mask=+Debug,+H2RxHdr,+H2RxBody",
		"-p", "h2_initial_window_size=1m",
		"-p", "h2_rx_window_low_water=64k",
		"-M", sock.Addr().String(),
	}
	for _, p := range vb.parameters {
		args = append(args, p.name, p.value)
	}

	pr, pw := io.Pipe()

	cmd := exec.Command("varnishd", args...)
	cmd.Stdout = pw
	cmd.Stderr = pw
	switch {
	case vb.licensePath != "":
		cmd.Env = append(os.Environ(), "VARNISH_LICENSE="+vb.licensePath)
	case os.Getenv("VARNISH_LICENSE") == "":
		cmd.Env = append(os.Environ(), "VARNISH_LICENSE=/usr/share/varnish-plus/vtc-license.dat")
	}

	err = cmd.Start()
	if err != nil {
		return
	}

	ss := newSyslogState(true, pw)
	ss.start(pr, cmd.Wait)
	vb.syslogs = ss

	var conn adm.Conn
	{
		type acceptResult struct {
			conn adm.Conn
			err  error
		}
		ch := make(chan acceptResult, 1)
		go func() {
			c, e := adm.Accept(sock, filepath.Join(name, "_.secret"))
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

			var name string
			var addr string
			var port int
			_, err := fmt.Sscanf(resp, "%s %s %d\n", &name, &addr, &port)
			if err != nil {
				return err
			}
			// FIXME: IPv6
			v.URL = fmt.Sprintf("http://%s:%d", addr, port)
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	return nil
}

// AdmRaw sends a command to the admin socket, with more control and less convenience.
// It's just a passthrough for [adm.Conn.AskRaw].
func (v *Varnish) AdmRaw(args ...string) (int, []byte, error) {
	return v.conn.AskRaw(args...)
}

// Adm sends a command to the admin socket.
// It's just a passthrough for [adm.Conn.Ask].
func (v *Varnish) Adm(args ...string) (string, error) {
	return v.conn.Ask(args...)
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
