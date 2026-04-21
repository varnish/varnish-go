package vtest_test

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"

	"github.com/varnish/varnish-go/adm"
	"github.com/varnish/varnish-go/vtest"
)

func TestSynth(t *testing.T) {
	// just a simple VCL with a synthetic response
	varnish, err := vtest.New().VclString(`
                backend default none;

                sub vcl_recv {
                        return(synth(200, "Good test"));
                }
        `).Start()
	if err != nil {
		t.Error(err)
		return
	}
	defer varnish.Stop()

	// use a regular client to send a request
	resp, err := http.Get(varnish.URL + "/test")
	// test the response using generic go facilities
	if err != nil {
		t.Error(err)
		return
	}

	if resp.Status != "200 Good test" {
		t.Errorf(`expected "200 Good test", got %s`, resp.Status)
	}
}

func TestBackend(t *testing.T) {
	// create a test backend
	svr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "this is my body")
	}))

	// add the backend definition to the loaded VCL
	varnish, err := vtest.New().Backend("svr", svr.URL).Start()
	if err != nil {
		t.Error(err)
		return
	}
	defer varnish.Stop()

	resp, err := http.Get(varnish.URL)
	if err != nil {
		t.Error(err)
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Error(err)
		return
	}
	if string(body) != "this is my body" {
		t.Errorf(`expected "200 Good test", got %s`, body)
	}
}

func TestRouting(t *testing.T) {
	// create a test backend
	svrA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("server", "A")
	}))
	svrB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("server", "B")
	}))

	// add the backend definition to the loaded VCL
	varnish, err := vtest.New().
		Backend("svrA", svrA.URL).
		Backend("svrB", svrB.URL).
		VclString(`
                                sub vcl_recv {
                                        if (req.url == "/A") {
                                                set req.backend_hint = svrA;
                                        } else {
                                                set req.backend_hint = svrB;
                                        }
                                }
        `).Start()
	if err != nil {
		t.Error(err)
		return
	}

	defer varnish.Stop()

	resp, err := http.Get(varnish.URL + "/A")
	if err != nil {
		t.Error(err)
		return
	}
	resp.Body.Close()

	if resp.Header.Get("server") != "A" {
		t.Errorf(`expected "A", got %s`, resp.Header.Get("server"))
		return
	}

	resp, err = http.Get(varnish.URL + "/B")
	if err != nil {
		t.Error(err)
		return
	}
	resp.Body.Close()

	if resp.Header.Get("server") != "B" {
		t.Errorf(`expected "B", got %s`, resp.Header.Get("server"))
		return
	}
}

func TestAdm(t *testing.T) {
	// just a simple VCL with a synthetic response
	varnish, err := vtest.New().VclString(`
                backend default none;

                sub vcl_recv {
                        return(synth(200, "Good test"));
                }
        `).Start()
	if err != nil {
		t.Error(err)
		return
	}
	defer varnish.Stop()

	// ask nicely (through the varnish object)
	msg, err := varnish.Adm("ping")
	if err != nil {
		t.Error(err)
		return
	}
	pong := strings.Fields(msg)[0]
	if pong != "PONG" {
		t.Errorf(`expected "PONG", got %s`, pong)
	}

	// ask directly via adm
	conn, err := adm.Connect(varnish.Name())
	if err != nil {
		t.Error(err)
		return
	}
	msg, err = conn.Ask("ping")
	if err != nil {
		t.Error(err)
		return
	}
	pong = strings.Fields(msg)[0]
	if pong != "PONG" {
		t.Errorf(`expected "PONG", got %s`, pong)
	}
}

const synthVCL = `
	backend default none;
	sub vcl_recv {
		return(synth(200, "ok"));
	}
`

// countBeginReq reads a binary VSL file and returns the number of client-request
// Begin entries, which equals the number of requests processed by Varnish.
func countBeginReq(t *testing.T, path string) int {
	t.Helper()
	out, err := exec.Command("varnishlog", "-r", path, "-d", "-g", "raw", "-i", "Begin").Output()
	if err != nil {
		t.Fatalf("varnishlog -r failed: %v", err)
	}
	count := 0
	for line := range strings.SplitSeq(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 4 && fields[1] == "Begin" && fields[3] == "req" {
			count++
		}
	}
	return count
}

func TestVarnishLogFile(t *testing.T) {
	f, err := os.CreateTemp("", "varnishlog-*.vsl")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())
	f.Close()

	varnish, err := vtest.New().VclString(synthVCL).VarnishLog(f).Start()
	if err != nil {
		t.Fatal(err)
	}

	const n = 20
	for range n {
		resp, err := http.Get(varnish.URL + "/test")
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
	}

	varnish.Stop()

	got := countBeginReq(t, f.Name())
	if got != n {
		t.Errorf("expected %d Begin req entries, got %d", n, got)
	}
}

func TestVarnishLogText(t *testing.T) {
	var buf strings.Builder

	varnish, err := vtest.New().VclString(synthVCL).VarnishLog(&buf).Start()
	if err != nil {
		t.Fatal(err)
	}

	const n = 20
	for range n {
		resp, err := http.Get(varnish.URL + "/test")
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
	}

	varnish.Stop()

	got := 0
	for line := range strings.SplitSeq(buf.String(), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 4 && fields[1] == "Begin" && fields[2] == "req" {
			got++
		}
	}
	if got != n {
		t.Errorf("expected %d Begin req lines, got %d\noutput:\n%s",
			n, got, buf.String())
	}
}

// TestVarnishLogCoverage runs multiple request counts to exercise the sleep-based
// varnihslog attachment window under load.
func TestVarnishLogCoverage(t *testing.T) {
	for _, n := range []int{1, 5, 50} {
		t.Run(strconv.Itoa(n)+"reqs", func(t *testing.T) {
			f, err := os.CreateTemp("", "varnishlog-*.vsl")
			if err != nil {
				t.Fatal(err)
			}
			defer os.Remove(f.Name())
			f.Close()

			varnish, err := vtest.New().VclString(synthVCL).VarnishLog(f).Start()
			if err != nil {
				t.Fatal(err)
			}

			for range n {
				resp, err := http.Get(varnish.URL + "/test")
				if err != nil {
					t.Fatal(err)
				}
				resp.Body.Close()
			}
			varnish.Stop()

			got := countBeginReq(t, f.Name())
			if got != n {
				t.Errorf("n=%d: expected %d Begin req entries, got %d", n, n, got)
			}
		})
	}
}

// Build a one-shot Varnish server, feed it a VCL and print the
// status of a GET request
func Example() {
	varnish, err := vtest.New().VclString(`
                backend default none;

                sub vcl_recv {
                        return(synth(200, "Good test"));
                }
        `).Start()
	if err != nil {
		panic(err)
	}
	defer varnish.Stop()

	// use a regular client to send a request
	resp, err := http.Get(varnish.URL + "/test")
	// test the response using generic go facilities
	if err != nil {
		panic(err)
	}

	fmt.Printf("status: %s\n", resp.Status)
}

// VarnishBuilder uses the builder pattern, each configuring function returning a pointer to the [VarnishBuilder]
// so that multiple functions can be chaind together.
func ExampleVarnishBuilder() {
	// add the backend definition to the loaded VCL
	varnish, err := vtest.New().
		Backend("primary", "http://1.2.3.4:8080").
		Backend("secondary", "http://5.6.7.8:8080").
		Parameter("connect_timeout", "10s").
		VclString(`
                          sub vcl_recv {
                                  if (req.http.host == "primary.varnish.local") {
                                          set req.backend_hint = primary;
                                  } else {
                                          set req.backend_hint = secondary;
                                  }
                          }
			  `).
		Start()
	if err != nil {
		panic(err)
	}
	defer varnish.Stop()
}

func ExampleVarnishBuilder_Backend() {
	// create a test backend
	svr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "this is my body")
	}))

	// add the backend definition to the loaded VCL
	varnish, err := vtest.New().Backend("svr", svr.URL).Start()
	if err != nil {
		panic(err)
	}
	defer varnish.Stop()

	resp, err := http.Get(varnish.URL)
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		panic(err)
	}

	fmt.Printf("response body: %s", string(body))
}
