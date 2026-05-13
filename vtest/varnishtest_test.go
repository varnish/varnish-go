package vtest_test

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/varnish/varnish-go/adm"
	"github.com/varnish/varnish-go/vtest"
)

func TestSynth(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()

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

func TestVarnishBuilder_AssertStart(t *testing.T) {
	varnish := vtest.New().VclString(`
                backend default none;
        `).AssertStart(t)
	defer varnish.Stop()
}

func ExampleVarnishBuilder_AssertStart() {
	t := &testing.T{}
	varnish := vtest.New().VclString(`
                backend default none;
        `).AssertStart(t)
	defer varnish.Stop()
	// Output:
	// main_test.go:31: vtest: Start: command: vcl.inline vcl1 << XXYYZZ
	//
	//                  vcl 4.1;
	//
	//                  backend default none;
	//
	//                  sub vcl_recv {
	//                          return(synxth(200, "OK"));
	//                  }
	//
	// 			        XXYYZZ
	//  failed with 106 status and message message:
	//  Message from VCC-compiler:
	//  Expected return action name.
	//        ('<vcl.inline>' Line 7 Pos 32)
	//                                return(synxth(200, "OK"));
	//        -------------------------------######-------------
	//
	//        Running VCC-compiler failed, exited with 2
	//        VCL compilation failed
	//
	//  Debug: Version: varnish-9.0.0 revision ce1b315b0c35477c666e4c8d8e1c9174df87eb61
	//  Debug: Platform: Linux,7.0.5-arch1-1,x86_64,-jnone,-sdefault,-sdefault,-hcritbit
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

	if err := varnish.Counter("MAIN.client_req").Equals(21); err != nil {
		panic(err)
	}
	if err := varnish.Counter("MAIN.s_synth").Equals(0); err != nil {
		panic(err)
	}
}

// VarnishBuilder uses the builder pattern, each configuring function returning a pointer to the [VarnishBuilder]
// so that multiple functions can be chained together.
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

func ExampleCounterChecker() {
	varnish, err := vtest.New().VclString(`
		backend default none;
		sub vcl_recv { return(synth(200, "OK")); }
	`).Start()
	if err != nil {
		panic(err)
	}
	defer varnish.Stop()

	if _, err := http.Get(varnish.URL + "/"); err != nil {
		panic(err)
	}

	if err := varnish.Counter("MAIN.client_req").Equals(1); err != nil {
		panic(err)
	}
	if err := varnish.Counter("MAIN.s_synth").AtLeast(1); err != nil {
		panic(err)
	}
}

func ExampleCounterChecker_WithTestFunction() {
	varnish, err := vtest.New().VclString(`
		backend default none;
		sub vcl_recv { return(synth(200, "OK")); }
	`).Start()
	if err != nil {
		panic(err)
	}
	defer varnish.Stop()

	for range 5 {
		if _, err := http.Get(varnish.URL + "/"); err != nil {
			panic(err)
		}
	}

	// Wait until client_req is an odd number.
	err = varnish.Counter("MAIN.client_req").WithTestFunction(func(v uint64) bool {
		return v%2 != 0
	})
	if err != nil {
		panic(err)
	}
}

func TestCounter(t *testing.T) {
	t.Parallel()
	varnish, err := vtest.New().VclString(`
		backend default none;
		sub vcl_recv {
			return(synth(200, "OK"));
		}
	`).Start()
	if err != nil {
		t.Fatal(err)
	}
	defer varnish.Stop()

	for range 3 {
		if _, err := http.Get(varnish.URL + "/test"); err != nil {
			t.Fatal(err)
		}
	}

	if err := varnish.Counter("MAIN.client_req").Equals(3); err != nil {
		t.Fatal(err)
	}

	if err := varnish.Counter("MAIN.does_not_exist").MustExist().Equals(0); err == nil {
		t.Error("expected error for unknown counter, got nil")
	}
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
