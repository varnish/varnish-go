// Package vtest provides facilities to build one-shot instances that you can test
// using regular golang HTTP entities.
// It's the "equivalent" of the [varnishtest] command
// but provides a more golang idiomatic interface.
//
// [varnishtest]: https://www.varnish-software.com/developers/tutorials/testing-varnish-varnishtest/
package vtest

import (
	"fmt"
	"log"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/google/uuid"

	"github.com/varnish/varnish-go/adm"
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

	parameters []parameter
	backends   []backend
}

// Varnish describes a running varnish instance, it must not be used once [Varnish.Stop] has been called.
type Varnish struct {
	// URL is the HTTP endpoint where Varnish is listening.
	// Varnish is started with a random port, discovered after startup.
	URL string

	cmd  *exec.Cmd
	name string
	conn adm.Conn
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

	cmd := exec.Command("varnishd", args...)

	err = cmd.Start()
	if err != nil {
		return
	}

	conn, err := adm.Accept(sock, filepath.Join(name, "_.secret"))
	if err != nil {
		return
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
		backendString := ""
		for _, b := range vb.backends {
			backendString += fmt.Sprintf(`backend %s {
	.host = "%s";
	.port = "%s";
	.host_header = "%s";
}
`, b.name, b.host, b.port, b.host)
		}

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

	return
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

// Stop stops and cleans the running Varnish instance.
// The caller must call this to avoid littered file systems and forever-running processes.
func (v *Varnish) Stop() {
	_, _ = v.Adm("stop")
	_ = v.conn.Close()

	if err := v.cmd.Process.Kill(); err != nil {
		log.Printf("failed to kill process: %s\n", err)
	}

	_ = os.RemoveAll(v.name)
}
