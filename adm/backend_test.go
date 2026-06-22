package adm_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/varnish/varnish-go/adm"
	"github.com/varnish/varnish-go/vtest"
)

func TestBackendList(t *testing.T) {
	t.Parallel()
	svr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer svr.Close()

	v := vtest.New().Backend("svr", svr.URL).AssertStart(t)
	defer v.Stop()

	backends, err := v.AdmConn().BackendList(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// BackendEntry{FullName: "vcl1.svr", VCL: "vcl1", Name: "svr", LastChange: non-zero}
	b, ok := backends["vcl1.svr"]
	if !ok {
		t.Fatal("backend vcl1.svr not found in BackendList")
	}
	if b.VCL != "vcl1" {
		t.Errorf("VCL: got %q, want \"vcl1\"", b.VCL)
	}
	if b.Name != "svr" {
		t.Errorf("Name: got %q, want \"svr\"", b.Name)
	}
	if b.LastChange.IsZero() {
		t.Error("LastChange is zero")
	}
}


func TestBackendListProbe(t *testing.T) {
	t.Parallel()
	svr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer svr.Close()

	u, _ := url.Parse(svr.URL)
	vcl := fmt.Sprintf(`
backend svr {
	.host = %q;
	.port = %q;
	.probe = {
		.url = "/";
		.timeout = 1s;
		.interval = 1s;
		.window = 3;
		.threshold = 2;
	}
}
`, u.Hostname(), u.Port())

	v := vtest.New().VclString(vcl).AssertStart(t)
	defer v.Stop()

	backends, err := v.AdmConn().BackendList(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	b, ok := backends["vcl1.svr"]
	if !ok {
		t.Fatal("backend vcl1.svr not found")
	}
	if b.Probe == nil {
		t.Fatal("Probe is nil — probe_message/probe_health not parsed")
	}
	if b.Probe.Total == 0 {
		t.Error("Probe.Total is 0 — window not parsed")
	}
	if b.LastChange.IsZero() {
		t.Error("LastChange is zero — last_change/last_updated not parsed")
	}
}

func TestBackendSetHealth(t *testing.T) {
	t.Parallel()
	svr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer svr.Close()

	v := vtest.New().Backend("svr", svr.URL).AssertStart(t)
	defer v.Stop()
	conn := v.AdmConn()
	ctx := context.Background()

	for _, state := range []adm.ProbeHealth{adm.ProbeHealthy, adm.ProbeSick, adm.ProbeProbe} {
		if err := conn.BackendSetHealth(ctx, "vcl1.svr", state); err != nil {
			t.Errorf("BackendSetHealth(vcl1.svr, %v): %v", state, err)
		}
	}

	// pattern validation errors
	for _, bad := range []string{"nodot", "too.many.dots", "inv@lid.pattern"} {
		if err := conn.BackendSetHealth(ctx, bad, adm.ProbeHealthy); err == nil {
			t.Errorf("expected error for pattern %q, got nil", bad)
		}
	}
	// ProbeUnknown is invalid
	if err := conn.BackendSetHealth(ctx, "vcl1.svr", adm.ProbeUnknown); err == nil {
		t.Error("expected error for ProbeUnknown state, got nil")
	}
}
