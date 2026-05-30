package adm_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/varnish/varnish-go/adm"
	"github.com/varnish/varnish-go/vtest"
)

const baseVCL = `backend default none; sub vcl_recv { return(synth(200, "OK")); }`

func TestStatus(t *testing.T) {
	t.Parallel()
	v := vtest.New().VclString(baseVCL).AssertStart(t)
	defer v.Stop()

	status, err := v.AdmConn().Status()
	if err != nil {
		t.Fatal(err)
	}
	if status != "running" {
		t.Errorf("got %q, want \"running\"", status)
	}
}

func TestPing(t *testing.T) {
	t.Parallel()
	v := vtest.New().VclString(baseVCL).AssertStart(t)
	defer v.Stop()

	if err := v.AdmConn().Ping(); err != nil {
		t.Fatal(err)
	}
}

func TestPID(t *testing.T) {
	t.Parallel()
	v := vtest.New().VclString(baseVCL).AssertStart(t)
	defer v.Stop()

	pid, err := v.AdmConn().PID()
	if err != nil {
		t.Fatal(err)
	}
	// PIDResponse{Master: >0, Worker: >0}
	if pid.Master == 0 {
		t.Error("Master PID is 0")
	}
	if pid.Worker == 0 {
		t.Error("Worker PID is 0")
	}
}

func TestBanner(t *testing.T) {
	t.Parallel()
	v := vtest.New().VclString(baseVCL).AssertStart(t)
	defer v.Stop()

	banner, err := v.AdmConn().Banner()
	if err != nil {
		t.Fatal(err)
	}
	if banner == "" {
		t.Error("Banner returned empty string")
	}
}

func TestPanic(t *testing.T) {
	t.Parallel()
	const panicVCL = `import vtc;
backend default none;
sub vcl_recv { vtc.panic("test panic"); }`
	v := vtest.New().VclString(panicVCL).AssertStart(t)
	defer v.Stop()
	conn := v.AdmConn()

	msg, err := conn.PanicShow()
	if err != nil {
		t.Fatal(err)
	}
	if msg != "" {
		t.Fatalf("expected no panic initially, got %q", msg)
	}

	http.Get(v.URL) //nolint:errcheck

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		status, _ := conn.Status()
		if status == "stopped" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	msg, err = conn.PanicShow()
	if err != nil {
		t.Fatal(err)
	}
	if msg == "" {
		t.Error("expected panic message after vtc.panic(), got empty string")
	}

	if err := conn.PanicClear(false); err != nil {
		t.Fatal(err)
	}
	if err := conn.PanicClear(true); err != nil {
		t.Fatal(err)
	}

	msg, err = conn.PanicShow()
	if err != nil {
		t.Fatal(err)
	}
	if msg != "" {
		t.Errorf("expected no panic after clear, got %q", msg)
	}
}

func TestStopStart(t *testing.T) {
	t.Parallel()
	v := vtest.New().VclString(baseVCL).AssertStart(t)
	defer v.Stop()
	conn := v.AdmConn()

	if err := conn.Stop(); err != nil {
		t.Fatal(err)
	}
	status, err := conn.Status()
	if err != nil {
		t.Fatal(err)
	}
	if status != "stopped" {
		t.Errorf("after Stop: got %q, want \"stopped\"", status)
	}

	if err := conn.Start(); err != nil {
		t.Fatal(err)
	}
	status, err = conn.Status()
	if err != nil {
		t.Fatal(err)
	}
	if status != "running" {
		t.Errorf("after Start: got %q, want \"running\"", status)
	}
}

func TestQuit(t *testing.T) {
	t.Parallel()
	v := vtest.New().VclString(baseVCL).AssertStart(t)
	defer v.Stop()

	// open a separate connection — Quit closes it, so we must not use AdmConn()
	conn, err := adm.Connect(v.Name())
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.Quit(); err != nil {
		t.Fatal(err)
	}
}
