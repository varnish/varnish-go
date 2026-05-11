package log_test

import (
	"context"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	varnishlog "github.com/varnish/varnish-go/log"
	"github.com/varnish/varnish-go/version"
)

// testBinPath returns the absolute path to the test VSL binary log fixture.
func testBinPath() string {
	_, self, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(self), "testdata", "test1_log.bin")
}

func skipIfEnterprise(t *testing.T) {
	t.Helper()
	if version.IsEnterprise() {
		t.Skip("test1_log.bin not compatible with Varnish Plus")
	}
}

func newFileReader(t *testing.T, grouping varnishlog.Grouping) *varnishlog.LogReader {
	t.Helper()
	skipIfEnterprise(t)
	r, err := varnishlog.New().
		SetGrouping(grouping).
		SetFile(testBinPath()).
		Attach()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(r.Close)
	return r
}

// TestFileGroupingVXID reads test1_log.bin with VXID grouping and verifies
// that every call to the handler receives exactly one transaction (the VXID
// grouping contract), and that all 8 VXID-distinct transactions in the file
// are delivered before Run returns.
func TestFileGroupingVXID(t *testing.T) {
	t.Parallel()
	r := newFileReader(t, varnishlog.GroupingVXID)

	var count int
	err := r.Run(context.Background(), func(txns []varnishlog.Transaction) error {
		if len(txns) != 1 {
			t.Errorf("GroupingVXID: expected 1 transaction per call, got %d", len(txns))
		}
		count++
		return nil
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	// test1_log.bin: sess 1, req 2, bereq 3, sess 32769, req 32770, bereq 32771, req 32772, bereq 32773
	const want = 8
	if count != want {
		t.Errorf("expected %d transactions, got %d", want, count)
	}
}

// TestFileGroupingRequest reads test1_log.bin with request grouping and verifies
// that exactly 2 request groups are delivered (one MISS and one PASS-with-restart).
func TestFileGroupingRequest(t *testing.T) {
	t.Parallel()
	r := newFileReader(t, varnishlog.GroupingRequest)

	type group struct {
		txnCount int
		hasBeReq bool
	}

	var groups []group
	err := r.Run(context.Background(), func(txns []varnishlog.Transaction) error {
		g := group{txnCount: len(txns)}
		for i := range txns {
			if txns[i].Type == varnishlog.TypeBackend {
				g.hasBeReq = true
			}
		}
		groups = append(groups, g)
		return nil
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(groups) != 2 {
		t.Fatalf("expected 2 request groups, got %d", len(groups))
	}

	// Group 1: req 2 + bereq 3 → 2 transactions
	if groups[0].txnCount != 2 {
		t.Errorf("group 1: expected 2 transactions, got %d", groups[0].txnCount)
	}
	if !groups[0].hasBeReq {
		t.Error("group 1: expected a backend transaction")
	}

	// Group 2: req 32770 + bereq 32771 + req 32772 + bereq 32773 → 4 transactions
	if groups[1].txnCount != 4 {
		t.Errorf("group 2: expected 4 transactions (pass+restart chain), got %d", groups[1].txnCount)
	}
	if !groups[1].hasBeReq {
		t.Error("group 2: expected backend transactions")
	}
}

// TestFileRunReturnsNilOnEOF verifies that Run returns nil (not a context error)
// once the file is fully consumed, without needing to cancel the context.
func TestFileRunReturnsNilOnEOF(t *testing.T) {
	t.Parallel()
	r := newFileReader(t, varnishlog.GroupingVXID)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- r.Run(ctx, func([]varnishlog.Transaction) error { return nil })
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run returned %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after file EOF within 5s")
	}
}

// TestFileQueryFilter verifies that a VSL query is applied when reading a file:
// only transactions matching the query should be delivered.
func TestFileQueryFilter(t *testing.T) {
	t.Parallel()
	skipIfEnterprise(t)
	r, err := varnishlog.New().
		SetGrouping(varnishlog.GroupingRequest).
		SetQuery(`ReqURL eq "/"`).
		SetFile(testBinPath()).
		Attach()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	reqURLTag, _ := varnishlog.TagByName("ReqURL")

	var sawRoot, sawUnknown bool
	err = r.Run(context.Background(), func(txns []varnishlog.Transaction) error {
		for _, txn := range txns {
			for _, rec := range txn.Records {
				if rec.Tag != reqURLTag {
					continue
				}
				switch rec.Data {
				case "/":
					sawRoot = true
				case "/unknown":
					sawUnknown = true
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !sawRoot {
		t.Error("expected to see ReqURL / through the query filter")
	}
	if sawUnknown {
		t.Error("expected ReqURL /unknown to be filtered out")
	}
}
