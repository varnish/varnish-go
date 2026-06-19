package adm_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/varnish/varnish-go/adm"
	"github.com/varnish/varnish-go/vtest"
)

// testdata paths for the pre-generated self-signed certificate.
// The cert is valid for 100 years from 2026-06-16.
//
// To regenerate:
//
//	openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:P-256 -keyout testdata/key.pem \
//	  -out testdata/cert.pem -days 36500 -nodes -subj "/CN=localhost" \
//	  -addext "subjectAltName=DNS:localhost,IP:127.0.0.1"
//	cat testdata/cert.pem testdata/key.pem > testdata/cert-and-key.pem
//
// Absolute paths are required because Varnish resolves paths from its own working directory.
var (
	tdCombined = mustAbs("testdata/cert-and-key.pem")
	tdCertOnly = mustAbs("testdata/cert.pem")
	tdKeyOnly  = mustAbs("testdata/key.pem")
)

func mustAbs(rel string) string {
	abs, err := filepath.Abs(rel)
	if err != nil {
		panic(err)
	}
	return abs
}

func skipIfTLSUnsupported(t *testing.T, err error) {
	t.Helper()
	if err != nil && strings.Contains(err.Error(), "Unknown request") {
		t.Skip("tls.cert.load not supported on this varnishd")
	}
}

func TestTLSCertLoadBasic(t *testing.T) {
	t.Parallel()

	v := vtest.New().TLSListener().NoRecordLogs().VclString(baseVCL).AssertStart(t)
	defer v.Stop()
	conn := v.AdmConn()
	ctx := context.Background()

	err := conn.TLSCertLoad(ctx, tdCombined)
	skipIfTLSUnsupported(t, err)
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.TLSCertCommit(ctx); err != nil {
		t.Fatal(err)
	}

	entries, err := conn.TLSCertList(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Error("TLSCertList: expected at least one entry after load+commit")
	}
}

func TestTLSCertLoadSeparateKey(t *testing.T) {
	t.Parallel()

	v := vtest.New().TLSListener().NoRecordLogs().VclString(baseVCL).AssertStart(t)
	defer v.Stop()
	conn := v.AdmConn()
	ctx := context.Background()

	err := conn.TLSCertLoad(ctx, tdCertOnly, adm.TLSWithKeyFile(tdKeyOnly))
	skipIfTLSUnsupported(t, err)
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.TLSCertCommit(ctx); err != nil {
		t.Fatal(err)
	}

	entries, err := conn.TLSCertList(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Error("TLSCertList: expected at least one entry after separate-key load+commit")
	}
}

func TestTLSCertLoadOptions(t *testing.T) {
	t.Parallel()

	v := vtest.New().TLSListener().NoRecordLogs().VclString(baseVCL).AssertStart(t)
	defer v.Stop()
	conn := v.AdmConn()
	ctx := context.Background()
	defer conn.TLSCertRollback(ctx) //nolint:errcheck — best-effort cleanup if probe or a subtest leaves staged state

	// probe support once before the table
	err := conn.TLSCertLoad(ctx, tdCombined)
	skipIfTLSUnsupported(t, err)
	if err != nil {
		t.Fatalf("probe load: %v", err)
	}
	if err := conn.TLSCertRollback(ctx); err != nil {
		t.Fatalf("probe rollback: %v", err)
	}

	cases := []struct {
		name     string
		certFile string
		opts     []adm.TLSOption
	}{
		{"cert-id", tdCombined, []adm.TLSOption{adm.TLSWithCertID("test-cert")}},
		{"frontend", tdCombined, []adm.TLSOption{adm.TLSWithFrontend("HTTPS")}},
		{"protocols", tdCombined, []adm.TLSOption{adm.TLSWithProtocols("TLSv1.2", "TLSv1.3")}},
		{"ciphers", tdCombined, []adm.TLSOption{adm.TLSWithCiphers("ECDHE-RSA-AES128-GCM-SHA256", "ECDHE-RSA-AES256-GCM-SHA384")}},
		{"cipher-suites", tdCombined, []adm.TLSOption{adm.TLSWithCipherSuites("TLS_AES_128_GCM_SHA256", "TLS_AES_256_GCM_SHA384")}},
		{"default-cert", tdCombined, []adm.TLSOption{adm.TLSWithDefaultCert()}},
		{"server-order", tdCombined, []adm.TLSOption{adm.TLSWithServerCipherOrder()}},
		{"multi", tdCertOnly, []adm.TLSOption{adm.TLSWithKeyFile(tdKeyOnly), adm.TLSWithDefaultCert(), adm.TLSWithServerCipherOrder()}},
	}

	// Subtests share conn and must run sequentially; t.Parallel() is intentionally absent.
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			defer conn.TLSCertRollback(ctx) //nolint:errcheck
			if err := conn.TLSCertLoad(ctx, tc.certFile, tc.opts...); err != nil {
				t.Fatalf("TLSCertLoad(%s): %v", tc.name, err)
			}
			if err := conn.TLSCertRollback(ctx); err != nil {
				t.Fatalf("TLSCertRollback(%s): %v", tc.name, err)
			}
		})
	}
}

func TestTLSCertLoadValidation(t *testing.T) {
	t.Parallel()

	// Validation fires before any network call; Varnish is started solely to obtain a *Conn.
	v := vtest.New().NoRecordLogs().VclString(baseVCL).AssertStart(t)
	defer v.Stop()
	conn := v.AdmConn()
	ctx := context.Background()

	cases := []struct {
		name    string
		opts    []adm.TLSOption
		wantErr string // expected substring in the error message
	}{
		{"cert-id-empty", []adm.TLSOption{adm.TLSWithCertID("")}, "TLSWithCertID"},
		{"cert-id-with-space", []adm.TLSOption{adm.TLSWithCertID("bad id")}, "TLSWithCertID"},
		{"frontend-empty", []adm.TLSOption{adm.TLSWithFrontend("")}, "TLSWithFrontend"},
		{"frontend-with-space", []adm.TLSOption{adm.TLSWithFrontend("bad name")}, "TLSWithFrontend"},
		{"keyfile-empty", []adm.TLSOption{adm.TLSWithKeyFile("")}, "TLSWithKeyFile"},
		{"cipher-with-space", []adm.TLSOption{adm.TLSWithCiphers("CIPHER ONE")}, "TLSWithCiphers"},
		{"cipher-with-colon", []adm.TLSOption{adm.TLSWithCiphers("CIPHER:ONE")}, "TLSWithCiphers"},
		{"cipher-empty-list", []adm.TLSOption{adm.TLSWithCiphers()}, "TLSWithCiphers"},
		{"ciphersuite-with-space", []adm.TLSOption{adm.TLSWithCipherSuites("TLS AES 128")}, "TLSWithCipherSuites"},
		{"ciphersuite-with-colon", []adm.TLSOption{adm.TLSWithCipherSuites("TLS:AES:128")}, "TLSWithCipherSuites"},
		{"ciphersuite-empty-list", []adm.TLSOption{adm.TLSWithCipherSuites()}, "TLSWithCipherSuites"},
		{"protocol-with-space", []adm.TLSOption{adm.TLSWithProtocols("TLS v1.3")}, "TLSWithProtocols"},
		{"protocol-with-comma", []adm.TLSOption{adm.TLSWithProtocols("TLSv1.2,TLSv1.3")}, "TLSWithProtocols"},
		{"protocol-empty-list", []adm.TLSOption{adm.TLSWithProtocols()}, "TLSWithProtocols"},
		// Early-exit: once the first option fails the loop stops; the second option is never applied.
		{"chained-cipher-then-suite", []adm.TLSOption{adm.TLSWithCiphers("BAD:ONE"), adm.TLSWithCipherSuites("ALSO:BAD")}, "TLSWithCiphers"},
		{"chained-cipher-then-protocol", []adm.TLSOption{adm.TLSWithCiphers("BAD:ONE"), adm.TLSWithProtocols("ok")}, "TLSWithCiphers"},
		{"chained-protocol-then-cipher", []adm.TLSOption{adm.TLSWithProtocols("BAD,ONE"), adm.TLSWithCiphers("CIPHER")}, "TLSWithProtocols"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// "cert.pem" does not exist — intentional: validation fires before any file I/O.
			err := conn.TLSCertLoad(ctx, "cert.pem", tc.opts...)
			if err == nil {
				t.Fatalf("%s: expected validation error, got nil", tc.name)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("%s: error %q does not contain %q", tc.name, err.Error(), tc.wantErr)
			}
		})
	}
}

func TestTLSCertListAndDiscard(t *testing.T) {
	t.Parallel()

	v := vtest.New().TLSListener().NoRecordLogs().VclString(baseVCL).AssertStart(t)
	defer v.Stop()
	conn := v.AdmConn()
	ctx := context.Background()

	err := conn.TLSCertLoad(ctx, tdCombined, adm.TLSWithCertID("discard-test"))
	skipIfTLSUnsupported(t, err)
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.TLSCertCommit(ctx); err != nil {
		t.Fatal(err)
	}

	entries, err := conn.TLSCertList(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, e := range entries {
		if e.ID == "discard-test" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("TLSCertList: expected entry with ID %q, got %+v", "discard-test", entries)
	}

	if err := conn.TLSCertDiscard(ctx, "discard-test"); err != nil {
		t.Fatal(err)
	}
	if err := conn.TLSCertCommit(ctx); err != nil {
		t.Fatal(err)
	}

	entries, err = conn.TLSCertList(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.ID == "discard-test" {
			t.Errorf("expected entry %q to be absent after TLSCertDiscard+commit, got %+v", e.ID, e)
		}
	}
}
