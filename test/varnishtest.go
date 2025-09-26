// This package provides facilities to build one-shot instances that you can test
// using regular golang HTTP entities.
// It's the "equivalent" of the [varnishtest] command
// but provides a more golang idiomatic interface.
//
// [varnishtest]: https://www.varnish-software.com/developers/tutorials/testing-varnish-varnishtest/
package test

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

// A configuration object collecting options before the actual Varnish instance is actually started.
type VarnishBuilder struct {
	vclIsFile  bool
	vclString  string
	vclVersion string

	parameters []parameter
	backends   []backend
}

// A running Varnish instance, it must not be use once [Varnish.Close] has been called.
type Varnish struct {
	// Varnish is started with a random port, you can use [Varnish.URL] to discover the listening endpoint.
	URL string

	cmd  *exec.Cmd
	name string
	conn adm.Conn
}

type socket struct {
	Endpoint string `json:"Endpoint"`
}

// Create a new builder, it will default to VCL version 4.1 and will provide no backend by default
func New() *VarnishBuilder {
	uv := &VarnishBuilder{}
	uv.Vcl41()
	return uv
}

// Provide a string containing the VCL to run. Note that the VCL version and backend definitions (according to [Varnish.Backend]) will be prepended to this string.
func (uv *VarnishBuilder) VclString(s string) *VarnishBuilder {
	uv.vclIsFile = false
	uv.vclString = s
	return uv
}

// Select a path to the VCL file to load
func (uv *VarnishBuilder) VclFile(s string) *VarnishBuilder {
	uv.vclIsFile = true
	uv.vclString = s
	return uv
}

// Append a parameter to the varnishd command
func (uv *VarnishBuilder) Parameter(name string, value string) *VarnishBuilder {
	uv.parameters = append(uv.parameters, parameter{name: name, value: value})
	return uv
}

// Set the VCL version to 4.1
func (uv *VarnishBuilder) Vcl41() *VarnishBuilder {
	uv.vclVersion = "vcl 4.1;\n\n"
	return uv
}

// Set the VCL version to 4.0
func (uv *VarnishBuilder) Vcl40() *VarnishBuilder {
	uv.vclVersion = "vcl 4.0;\n\n"
	return uv
}

// Set the VCL version to the value of version
func (uv *VarnishBuilder) VCLVersion(version string) *VarnishBuilder {
	uv.vclVersion = version
	return uv
}

// Create a VCL backend definition. Name must be a valid VCL backend name, otherwise Varnish will fail to start.
// This call will panic if urlRaw isn't parsable into a [net.URL]
func (uv *VarnishBuilder) Backend(name string, urlRaw string) *VarnishBuilder {
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

	uv.backends = append(uv.backends, backend{
		name,
		host,
		port,
		ssl,
	})
	return uv
}

// Start a Varnish instance using the options specified in VarnishBuilder. The VarnishBuilder point must not be used after this calling this function.
func (uv *VarnishBuilder) Start() (varnish Varnish, err error) {
	sock, err := net.Listen("tcp", ":0")
	if err != nil {
		return
	}

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
	for _, p := range uv.parameters {
		args = append(args, p.name, p.value)
	}

	cmd := exec.Command("varnishd",
		args...,
	)

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

	if uv.vclIsFile {
		_, err = varnish.Adm("vcl.load", "vcl1", uv.vclString)
		if err != nil {
			return
		}
	} else {
		backendString := ""
		for _, b := range uv.backends {
			backendString += fmt.Sprintf(`backend %s {
	.host = "%s";
	.port = "%s";
	.host_header = "%s";
}
`, b.name, b.host, b.port, b.host)
		}

		vcl := fmt.Sprintf("%s%s%s", uv.vclVersion, backendString, uv.vclString)
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

	varnish.WaitRunning()

	return
}

// Blocking function that will return only when the Varnish child is running. You should generally not need it as
// this is already called as part of [VarnishBuilder.Start].
func (varnish *Varnish) WaitRunning() error {
	resp, err := varnish.Adm("status")
	for {
		if err != nil {
			return err
		}
		if string(resp) == "Child in state stopped" {
			return fmt.Errorf("Child stopped before running")
		}
		if string(resp) == "Child in state running\n" {
			resp, err = varnish.Adm("debug.listen_address")
			if err != nil {
				return err
			}

			var name string
			var addr string
			var port int
			_, err := fmt.Sscanf(string(resp), "%s %s %d\n", &name, &addr, &port)
			if err != nil {
				return err
			}
			// FIXME: IPv6
			varnish.URL = fmt.Sprintf("http://%s:%d", addr, port)
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	return nil
}

// Send a command to the admin socket, with more control and less convenience. It's just a passthrough for [adm.Conn.AskRaw].
func (varnish *Varnish) AdmRaw(args ...string) (int, []byte, error) {
	return varnish.conn.AskRaw(args...)
}

// Send a command to the admin socket. It's just a passthrough for [adm.Conn.Ask].
func (varnish *Varnish) Adm(args ...string) (string, error) {
	return varnish.conn.Ask(args...)
}

// Stop and clean the running Varnish instance. The call must call this to avoid littered file systems and forever-running processes.
func (varnish *Varnish) Stop() {
	varnish.Adm("stop")
	varnish.conn.Close()

	if err := varnish.cmd.Process.Kill(); err != nil {
		log.Printf("failed to kill process: %s\n", err)
	}

	os.RemoveAll(varnish.name)
}
