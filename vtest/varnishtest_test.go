package vtest_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	conn, err := adm.Connect(context.Background(), varnish.Name())
	if err != nil {
		t.Error(err)
		return
	}
	msg, err = conn.Ask(context.Background(), "ping")
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

				sub vcl_recv {
					return(synth(200, "OK"));
				}
        `).AssertStart(t)
	defer varnish.Stop()
}

func TestVarnishBuilder_Start_BadVCL(t *testing.T) {
	t.Parallel()
	_, err := vtest.New().VclString(`
		backend default none;
		sub vcl_recv {
			return(invalid_action);
		}
	`).Start()
	if err == nil {
		t.Fatal("expected error for bad VCL, got nil")
	}
	if !strings.Contains(err.Error(), "VCL compilation failed") {
		t.Errorf("expected compilation error in message, got: %v", err)
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

func generateSelfSignedCert(t *testing.T) (certFile, keyFile string) {
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

	dir := t.TempDir()
	certFile = filepath.Join(dir, "cert.pem")
	keyFile = filepath.Join(dir, "key.pem")

	if err := os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER}), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: privDER}), 0600); err != nil {
		t.Fatal(err)
	}

	return certFile, keyFile
}

func TestVarnishBuilder_SetEnv(t *testing.T) {
	t.Parallel()

	vb := vtest.New().
		SetEnv("MY_VAR", "myvalue").
		VclString(`
			import std;

			backend default none;
			sub vcl_recv {
				return(synth(200, "OK"));
			}
			sub vcl_synth {
				set resp.http.My-Var = std.getenv("MY_VAR");
			}
		`)
	varnish, err := vb.Start()
	if err != nil {
		t.Fatalf("%v\n%s", err, strings.Join(vb.SysLogs(), "\n"))
	}
	defer varnish.Stop()

	resp, err := http.Get(varnish.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if got := resp.Header.Get("My-Var"); got != "myvalue" {
		t.Errorf(`expected My-Var header "myvalue", got %q`, got)
	}
}

func TestVarnishBuilder_SetEnv_Replace(t *testing.T) {
	t.Parallel()

	vb := vtest.New().
		SetEnv("MY_VAR", "first").
		SetEnv("MY_VAR", "second").
		VclString(`
			import std;

			backend default none;
			sub vcl_recv {
				return(synth(200, "OK"));
			}
			sub vcl_synth {
				set resp.http.My-Var = std.getenv("MY_VAR");
			}
		`)
	varnish, err := vb.Start()
	if err != nil {
		t.Fatalf("%v\n%s", err, strings.Join(vb.SysLogs(), "\n"))
	}
	defer varnish.Stop()

	resp, err := http.Get(varnish.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if got := resp.Header.Get("My-Var"); got != "second" {
		t.Errorf(`expected My-Var header "second", got %q`, got)
	}
}

func TestVarnishBuilder_SetLicensePath_OverridesSetEnv(t *testing.T) {
	t.Parallel()

	vb := vtest.New().
		SetEnv("VARNISH_LICENSE", "/via/setenv").
		SetLicensePath("/via/setlicensepath").
		VclString(`
			import std;

			backend default none;
			sub vcl_recv {
				return(synth(200, "OK"));
			}
			sub vcl_synth {
				set resp.http.License = std.getenv("VARNISH_LICENSE");
			}
		`)
	varnish, err := vb.Start()
	if err != nil {
		t.Fatalf("%v\n%s", err, strings.Join(vb.SysLogs(), "\n"))
	}
	defer varnish.Stop()

	resp, err := http.Get(varnish.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if got := resp.Header.Get("License"); got != "/via/setlicensepath" {
		t.Errorf(`expected License header "/via/setlicensepath", got %q`, got)
	}
}

func TestVarnishBuilder_SetEnv_InvalidKey(t *testing.T) {
	t.Parallel()

	badKeys := []string{
		"",
		"FOO=BAR",
		"FOO\x00BAR",
		"FOO BAR",
		"1FOO",
		"FOO-BAR",
	}

	for _, key := range badKeys {
		key := key
		t.Run(key, func(t *testing.T) {
			t.Parallel()
			_, err := vtest.New().SetEnv(key, "v").Start()
			if err == nil {
				t.Fatalf("expected error for invalid key %q, got nil", key)
			}
		})
	}
}

func TestVarnishBuilder_ClearEnv(t *testing.T) {
	t.Parallel()

	// A minimal PATH must be set back explicitly: VCL compilation shells out
	// to a C compiler, which ClearEnv's blank slate would otherwise hide.
	// A fixed value is used rather than the live process's PATH.
	vb := vtest.New().
		SetEnv("PRE_CLEAR_VAR", "shouldnotexist").
		ClearEnv().
		SetEnv("PATH", "/usr/bin:/bin").
		SetEnv("MY_VAR", "myvalue").
		VclString(`
		import std;

		backend default none;
		sub vcl_recv {
			return(synth(200, "OK"));
		}
		sub vcl_synth {
			set resp.http.My-Var = std.getenv("MY_VAR");
			set resp.http.Pre-Clear-Var = std.getenv("PRE_CLEAR_VAR");
		}
	`)
	varnish, err := vb.Start()
	if err != nil {
		t.Fatalf("%v\n%s", err, strings.Join(vb.SysLogs(), "\n"))
	}
	defer varnish.Stop()

	resp, err := http.Get(varnish.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if got := resp.Header.Get("My-Var"); got != "myvalue" {
		t.Errorf(`expected My-Var header "myvalue", got %q`, got)
	}
	if got := resp.Header.Get("Pre-Clear-Var"); got != "" {
		t.Errorf(`expected Pre-Clear-Var header to be empty after ClearEnv, got %q`, got)
	}
}

func TestTLSListener(t *testing.T) {
	t.Parallel()

	certFile, keyFile := generateSelfSignedCert(t)

	vb := vtest.New().
		PEMFile(certFile, keyFile).
		VclString(`
			backend default none;
			sub vcl_recv {
				return(synth(200, "TLS OK"));
			}
		`)
	varnish, err := vb.Start()
	if err != nil {
		t.Fatalf("%v\n%s", err, strings.Join(vb.SysLogs(), "\n"))
	}
	defer varnish.Stop()

	if varnish.TLSURL == "" {
		t.Fatal("TLSURL is empty after TLSListener()+PEMFile()")
	}

	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec
		},
	}
	resp, err := client.Get(varnish.TLSURL + "/test")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}
