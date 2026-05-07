package stat_test

import (
	"fmt"
	"log"
	"net/http"
	"testing"
	"time"
	"unsafe"

	"github.com/varnish/varnish-go/stat"
	"github.com/varnish/varnish-go/vtest"
)

func ExampleStatReader_Counters() {
	r, err := stat.New().Attach()
	if err != nil {
		log.Fatal(err)
	}
	defer r.Close()

	r.Update()
	for name, c := range r.Counters() {
		fmt.Printf("%s %d\n", name, c.Value)
	}
}

func ExampleStatReader_Update() {
	r, err := stat.New().
		SetName("/var/lib/varnish/myinstance").
		SetTimeout(5 * time.Second).
		Attach()
	if err != nil {
		log.Fatal(err)
	}
	defer r.Close()

	for {
		time.Sleep(time.Second)
		r.Update()
		if val, ok := r.CounterValue("MAIN.client_req"); ok {
			fmt.Println(val)
		}
	}
}

func startVarnish(t *testing.T) vtest.Varnish {
	t.Helper()
	v, err := vtest.New().VclString(`
		backend default none;
		sub vcl_recv {
			return(synth(200, "OK"));
		}
	`).Start()
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func newStatReader(t *testing.T, v *vtest.Varnish) *stat.StatReader {
	t.Helper()
	r, err := stat.New().
		SetName(v.Name()).
		SetTimeout(5 * time.Second).
		Attach()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(r.Close)
	return r
}

func mustUpdate(t *testing.T, r *stat.StatReader) (added, removed []unsafe.Pointer) {
	t.Helper()
	added, removed, err := r.Update()
	if err != nil {
		t.Fatal(err)
	}
	return added, removed
}

func TestUpdateAddsCountersOnFirstCall(t *testing.T) {
	v := startVarnish(t)
	defer v.Stop()

	c := newStatReader(t, &v)
	added, removed := mustUpdate(t, c)

	if len(added) == 0 {
		t.Error("expected counters to be added on first Update")
	}
	if len(removed) != 0 {
		t.Errorf("expected no counters removed on first Update, got %d", len(removed))
	}
}

func TestUpdateStabilizes(t *testing.T) {
	v := startVarnish(t)
	defer v.Stop()

	c := newStatReader(t, &v)

	// Varnish's child process registers its VSM segments shortly after the
	// manager's, so the first few Updates may still report new counters.
	// Drain until the set is stable.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		added, _ := mustUpdate(t, c)
		if len(added) == 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Once stable, a further Update must report no changes.
	added, removed := mustUpdate(t, c)
	if len(added) != 0 {
		t.Errorf("expected no new counters after stabilization, got %d", len(added))
	}
	if len(removed) != 0 {
		t.Errorf("expected no removed counters after stabilization, got %d", len(removed))
	}
}

func TestCounter(t *testing.T) {
	v := startVarnish(t)
	defer v.Stop()

	c := newStatReader(t, &v)
	mustUpdate(t, c)

	counter, _ := c.Counter("MAIN.cache_hit")
	if counter.Name != "MAIN.cache_hit" {
		t.Fatalf("expected MAIN.cache_hit, got %q", counter.Name)
	}
	if counter.Semantics != stat.SemanticsCounter {
		t.Errorf("expected SemanticsCounter, got %v", counter.Semantics)
	}
	if counter.Flags != stat.FlagsInteger {
		t.Errorf("expected FlagsInteger, got %v", counter.Flags)
	}

	if counter, ok := c.Counter("MAIN.does_not_exist"); ok {
		t.Errorf("expected no Counter for unknown name, got %v", counter)
	}
}

func TestCounters(t *testing.T) {
	v := startVarnish(t)
	defer v.Stop()

	c := newStatReader(t, &v)
	mustUpdate(t, c)

	counters := c.Counters()
	if len(counters) == 0 {
		t.Fatal("expected counters, got none")
	}
	if _, ok := counters["MAIN.cache_hit"]; !ok {
		t.Error("expected MAIN.cache_hit in counters")
	}
}

func TestCounterValue(t *testing.T) {
	v := startVarnish(t)
	defer v.Stop()

	c := newStatReader(t, &v)

	for range 3 {
		if _, err := http.Get(v.URL + "/test"); err != nil {
			t.Fatal(err)
		}
	}

	time.Sleep(100 * time.Millisecond)
	mustUpdate(t, c)

	if val, _ := c.CounterValue("MAIN.client_req"); val != 3 {
		t.Errorf("expected MAIN.client_req != 3 after 3 requests, got %d", val)
	}
}
