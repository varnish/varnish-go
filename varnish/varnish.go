// Package varnish provides facilities to start, configure and manage a
// varnishd child process from a long-running Go application.
package varnish

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/varnish/varnish-go/adm"
	vsl "github.com/varnish/varnish-go/log"
	"github.com/varnish/varnish-go/stat"
	"github.com/varnish/varnish-go/version"
)

type parameter struct {
	name  string
	value string
}

// VarnishBuilder is a configuration object collecting options before the actual Varnish instance is started.
type VarnishBuilder struct {
	vclSet    bool
	vclIsFile bool
	vclFile   string
	vclString string

	addresses          []string
	listenerNames      map[string]struct{}
	httpsListeners     []httpsListener
	jail               string
	readOnlyParameters []string
	parameters         []parameter
	output             io.Writer

	workDir     string
	stopTimeout time.Duration

	licensePath string
	environ     []string

	buildErr error

	syslogs *syslogState
}

var envKeyRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// defaultWorkDir is the varnishd instance name/working directory (-n) used
// when [VarnishBuilder.WorkDir] is not called: a stable, known location
// suitable for a long-running application. It is never removed by
// [Varnish.Stop].
const defaultWorkDir = "/var/lib/varnish/varnishd"

// New creates a new VarnishBuilder with default settings: no VCL loaded, a
// 5s [VarnishBuilder.StopTimeout], and [VarnishBuilder.WorkDir] defaulting
// to [defaultWorkDir].
func New() *VarnishBuilder {
	return &VarnishBuilder{
		environ:     os.Environ(),
		stopTimeout: 5 * time.Second,
		workDir:     defaultWorkDir,
	}
}

// VclFile selects the path to the VCL file to load. The file must be
// self-contained (including its own "vcl 4.1;" version line and any backend
// definitions).
func (vb *VarnishBuilder) VclFile(path string) *VarnishBuilder {
	vb.vclSet = true
	vb.vclIsFile = true
	vb.vclFile = path
	return vb
}

// VclString provides a string containing the VCL to run, loaded via
// vcl.inline. The string must be self-contained (including its own
// "vcl 4.1;" version line and any backend definitions).
func (vb *VarnishBuilder) VclString(s string) *VarnishBuilder {
	vb.vclSet = true
	vb.vclIsFile = false
	vb.vclString = s
	return vb
}

// ReadOnlyParameter marks a parameter as read-only. The parameter cannot be changed at runtime via varnishadm.
func (vb *VarnishBuilder) ReadOnlyParameter(names ...string) *VarnishBuilder {
	vb.readOnlyParameters = append(vb.readOnlyParameters, names...)
	return vb
}

// Address adds a raw listener address to the Varnish instance, passed
// through to varnishd's -a flag as-is.
//
// Deprecated: prefer [VarnishBuilder.HTTPListener], [VarnishBuilder.HTTPSListener],
// or [VarnishBuilder.UDSSocket], which validate their input and check for
// name collisions. Address remains for listener shapes those don't cover
// (e.g. PROXY protocol).
func (vb *VarnishBuilder) Address(addresses ...string) *VarnishBuilder {
	log.Printf("varnish: Address is deprecated, prefer HTTPListener/HTTPSListener/UDSSocket")
	vb.addresses = append(vb.addresses, addresses...)
	return vb
}

// Jail sets the jail mechanism to use.
func (vb *VarnishBuilder) Jail(jail string) *VarnishBuilder {
	vb.jail = jail
	return vb
}

// Parameter appends a -p name=value startup parameter to the varnishd
// command line. Parameters that are runtime-settable can also be changed
// after start via [Varnish.AdmConn] and [adm.Conn.ParamSet].
func (vb *VarnishBuilder) Parameter(name string, value string) *VarnishBuilder {
	vb.parameters = append(vb.parameters, parameter{name: name, value: value})
	return vb
}

// tlsProto returns the protocol name for the TLS listener flag.
// Varnish Enterprise uses "https"; Varnish Cache uses "TLS".
func tlsProto() string {
	if version.IsEnterprise() {
		return "https"
	}
	return "TLS"
}

// WorkDir sets the varnishd instance name/working directory (-n), created
// via MkdirAll if missing. Defaults to [defaultWorkDir]. [Varnish.Stop]
// never removes it — that's the caller's responsibility (see the vtest
// package for an example that manages its own ephemeral, self-cleaning
// workdir on top of this).
func (vb *VarnishBuilder) WorkDir(name string) *VarnishBuilder {
	vb.workDir = name
	return vb
}

// StopTimeout sets how long [Varnish.Stop] waits for a graceful shutdown
// before force-killing the process. Default: 5s.
func (vb *VarnishBuilder) StopTimeout(d time.Duration) *VarnishBuilder {
	vb.stopTimeout = d
	return vb
}

// Output sets a writer that receives the combined stdout/stderr stream of the
// varnishd process, in addition to the diagnostics [VarnishBuilder.Build]
// captures internally. Optional; mainly useful for callers (such as vtest)
// that want to build their own log accumulation or streaming on top.
func (vb *VarnishBuilder) Output(w io.Writer) *VarnishBuilder {
	vb.output = w
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

// SetEnv sets an environment variable for the varnishd process. Setting the
// same key again replaces its previous value.
// If key does not follow POSIX environment variable name syntax
// ([A-Za-z_][A-Za-z0-9_]*), the error is recorded and returned by
// [VarnishBuilder.Build].
func (vb *VarnishBuilder) SetEnv(key, value string) *VarnishBuilder {
	if !envKeyRE.MatchString(key) {
		if vb.buildErr == nil {
			vb.buildErr = fmt.Errorf("varnish: SetEnv: invalid key %q: must match POSIX env name syntax ([A-Za-z_][A-Za-z0-9_]*)", key)
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

// Varnish describes a running varnish instance, it must not be used once [Varnish.Stop] has been called.
type Varnish struct {
	// URL is the HTTP endpoint where Varnish is listening, populated when a
	// listener named "HTTP" is configured (e.g. via
	// [VarnishBuilder.HTTPListener]), discovered after startup.
	URL string

	// TLSURL is the HTTPS endpoint where Varnish is listening, populated
	// when a listener named "HTTPS" is configured (e.g. via
	// [VarnishBuilder.HTTPSListener]).
	TLSURL string

	// listeners maps every named listener to its resolved "host:port"
	// address. The map always contains the "HTTP" entry and, when
	// [VarnishBuilder.HTTPSListener] was called, the "HTTPS" entry. Extra
	// listeners added via [VarnishBuilder.Address] with a NAME=addr
	// syntax are included under their given name.
	listeners map[string]string

	cmd         *exec.Cmd
	name        string
	stopTimeout time.Duration
	conn        *adm.Conn
	syslogs     *syslogState
}

// ListenAddr returns the resolved "host:port" for the named listener, or
// an empty string if the name is unknown. IPv6 addresses are
// bracket-wrapped so the result can be embedded directly in a URL
// (e.g. "http://" + v.ListenAddr("IPv6") + "/path").
func (v *Varnish) ListenAddr(name string) string {
	return v.listeners[name]
}

// Build starts a Varnish instance using the options specified in VarnishBuilder.
// The VarnishBuilder pointer must not be used after calling this function.
func (vb *VarnishBuilder) Build() (varnish Varnish, err error) {
	if vb.buildErr != nil {
		return varnish, vb.buildErr
	}
	if len(vb.addresses) == 0 {
		err = fmt.Errorf("varnish: at least one listener required (HTTPListener, HTTPSListener, or UDSSocket)")
		return
	}
	sock, err := net.Listen("tcp", ":0")
	if err != nil {
		return
	}
	defer sock.Close()

	name := vb.workDir
	if err = os.MkdirAll(name, 0o755); err != nil {
		return
	}

	args := []string{}
	if vb.jail != "" {
		args = append(args, "-j", vb.jail)
	}
	args = append(args,
		"-F",
		"-f", "",
		"-n", name,
		"-M", sock.Addr().String(),
	)
	for _, p := range vb.parameters {
		args = append(args, "-p", p.name+"="+p.value)
	}
	if len(vb.readOnlyParameters) > 0 {
		args = append(args, "-r", strings.Join(vb.readOnlyParameters, ","))
	}
	for _, a := range vb.addresses {
		args = append(args, "-a", a)
	}

	pr, pw := io.Pipe()

	var stdout io.Writer = pw
	if vb.output != nil {
		stdout = io.MultiWriter(pw, vb.output)
	}

	cmd := exec.Command("varnishd", args...)
	cmd.Stdout = stdout
	cmd.Stderr = stdout
	cmd.Env = vb.environ
	if vb.licensePath != "" {
		cmd.Env = setEnvVar(cmd.Env, "VARNISH_LICENSE", vb.licensePath)
	}

	err = cmd.Start()
	if err != nil {
		return
	}

	ss := newSyslogState(pw)
	ss.start(pr, cmd.Wait)
	vb.syslogs = ss

	// Enrich a failed Build with whatever diagnostics were captured.
	// Registered before the process-killing defer below, so it runs last
	// (LIFO) and sees output captured up to the point the process exited.
	defer func() {
		if err == nil {
			return
		}
		if logs := ss.snapshot(); len(logs) > 0 {
			err = fmt.Errorf("%w\n%s", err, strings.Join(logs, "\n"))
		}
	}()

	// From here on, any failed step must not leak the varnishd process.
	// Registered after the workdir removal above, so it runs first (LIFO):
	// the process is killed and reaped before the workdir is removed.
	defer func() {
		if err == nil {
			return
		}
		if varnish.conn != nil {
			_ = varnish.conn.Close()
		}
		_ = cmd.Process.Kill()
		<-ss.exited
	}()

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
			err = fmt.Errorf("varnish: varnishd exited before connecting to management socket")
			return
		}
	}

	varnish = Varnish{
		cmd:         cmd,
		name:        name,
		stopTimeout: vb.stopTimeout,
		conn:        conn,
	}

	ctx := context.Background()

	// Without a VCL, there's nothing to run: leave the master process up in
	// management mode, VCL-less and childless, for the caller to load and
	// start manually via Adm.
	if vb.vclSet {
		if vb.vclIsFile {
			err = conn.VCLLoad(ctx, "vcl1", vb.vclFile, adm.VCLStateAuto)
		} else {
			err = conn.VCLInline(ctx, "vcl1", vb.vclString, adm.VCLStateAuto)
		}
		if err != nil {
			return
		}
		if err = conn.VCLUse(ctx, "vcl1"); err != nil {
			return
		}
		if err = conn.Start(ctx); err != nil {
			return
		}

		err = varnish.WaitRunning()
		if err != nil {
			return
		}
	}

	certsLoaded := false
	for _, hl := range vb.httpsListeners {
		for _, pem := range hl.pemFiles {
			if err = conn.TLSCertLoad(ctx, pem); err != nil {
				return
			}
			certsLoaded = true
		}
	}
	if certsLoaded {
		if err = conn.TLSCertCommit(ctx); err != nil {
			return
		}
	}

	ss.finalize()
	varnish.syslogs = ss
	vb.syslogs = nil

	return
}

// Name returns the workdir path.
func (v *Varnish) Name() string {
	return v.name
}

// LogReaderBuilder returns a [vsl.LogReaderBuilder] pre-configured with this
// instance's name, ready for further configuration and Attach.
func (v *Varnish) LogReaderBuilder() *vsl.LogReaderBuilder {
	return vsl.New().SetName(v.name)
}

// StatReaderBuilder returns a [stat.StatReaderBuilder] pre-configured with
// this instance's name, ready for further configuration and Attach.
func (v *Varnish) StatReaderBuilder() *stat.StatReaderBuilder {
	return stat.New().SetName(v.name)
}

// WaitRunning blocks until the Varnish child is running.
// You should generally not need this as it is already called as part of [VarnishBuilder.Build].
func (v *Varnish) WaitRunning() error {
	ctx := context.Background()
	for {
		status, err := v.conn.Status(ctx)
		if err != nil {
			return err
		}
		if status == "stopped" {
			return fmt.Errorf("child stopped before running")
		}
		if status == "running" {
			// No typed wrapper for this debug command.
			resp, err := v.conn.Ask(ctx, "debug.listen_address")
			if err != nil {
				return err
			}

			// Each line is either "name host port" (TCP listeners) or
			// "name path -" (Unix domain sockets, no port to parse).
			v.listeners = make(map[string]string)
			for _, line := range strings.Split(strings.TrimSpace(resp), "\n") {
				var lname, laddr string
				var lport int
				if _, scanErr := fmt.Sscanf(line, "%s %s %d", &lname, &laddr, &lport); scanErr != nil {
					continue
				}
				hostPort := net.JoinHostPort(laddr, strconv.Itoa(lport))
				v.listeners[lname] = hostPort
				switch lname {
				case "HTTP":
					v.URL = "http://" + hostPort
				case "HTTPS":
					v.TLSURL = "https://" + hostPort
				}
			}
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	return nil
}

// AdmConn returns the underlying admin connection.
func (v *Varnish) AdmConn() *adm.Conn {
	return v.conn
}

// Stop asks the running Varnish instance to shut down gracefully by sending
// SIGTERM to the manager process (which itself stops the child before
// exiting), force-killing it after [VarnishBuilder.StopTimeout] if it
// doesn't exit on its own. The workdir is never removed; that's the
// caller's responsibility.
func (v *Varnish) Stop() {
	_ = v.cmd.Process.Signal(syscall.SIGTERM)

	if v.syslogs != nil {
		select {
		case <-v.syslogs.exited:
		case <-time.After(v.stopTimeout):
			if err := v.cmd.Process.Kill(); err != nil {
				log.Printf("varnish: failed to kill process: %s\n", err)
			}
			<-v.syslogs.exited
		}
	} else if err := v.cmd.Process.Kill(); err != nil {
		log.Printf("varnish: failed to kill process: %s\n", err)
	}

	_ = v.conn.Close()

	if v.syslogs != nil {
		v.syslogs.stop()
	}
}
