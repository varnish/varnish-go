package adm_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/varnish/varnish-go/adm"
	"github.com/varnish/varnish-go/vtest"
)

// vclTempFile creates a *.vcl temp file that varnishd can read even when running as a different user.
// The temp directory and its parent are chmod'd to 0755.
func vclTempFile(t *testing.T) *os.File {
	t.Helper()
	dir := t.TempDir()
	for _, d := range []string{filepath.Dir(dir), dir} {
		if err := os.Chmod(d, 0755); err != nil {
			t.Fatal(err)
		}
	}
	f, err := os.CreateTemp(dir, "*.vcl")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(f.Name(), 0644); err != nil {
		t.Fatal(err)
	}
	return f
}

func TestVCLTemperatureString(t *testing.T) {
	cases := []struct {
		in   adm.VCLTemperature
		want string
	}{
		{adm.VCLTempWarm, "warm"},
		{adm.VCLTempCold, "cold"},
		{adm.VCLTempInit, "init"},
		{adm.VCLTempBusy, "busy"},
		{adm.VCLTempCooling, "cooling"},
		{adm.VCLTempUnknown, "unknown"},
	}
	for _, c := range cases {
		if got := c.in.String(); got != c.want {
			t.Errorf("VCLTemperature(%d).String() = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestVCLList(t *testing.T) {
	t.Parallel()
	v := vtest.New().VclString(baseVCL).AssertStart(t)
	defer v.Stop()
	conn := v.AdmConn()
	ctx := context.Background()

	const vcl2src = "vcl 4.1;\nbackend default none;\nsub vcl_recv { return(synth(200, \"vcl2\")); }\n"
	if err := conn.VCLInline(ctx, "vcl2", vcl2src, adm.VCLStateAuto); err != nil {
		t.Fatal(err)
	}
	defer conn.VCLDiscard(ctx, "vcl2")

	// map["vcl1": {Status: "active", Temperature: != VCLTempUnknown}, "vcl2": {Status: "available", ...}]
	entryMap, err := conn.VCLList(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// VCLEntry{Name: "vcl1", Status: "active", State: "auto", Temperature: VCLTempWarm, Busy: 0}
	if e, ok := entryMap["vcl1"]; !ok {
		t.Error("vcl1 not found in VCLList")
	} else if want := (adm.VCLEntry{Name: "vcl1", Status: "active", State: "auto", Temperature: adm.VCLTempWarm}); !reflect.DeepEqual(e, want) {
		t.Errorf("vcl1: got %+v, want %+v", e, want)
	}
	// VCLEntry{Name: "vcl2", Status: "available", State: "auto", Temperature: VCLTempWarm, Busy: 0}
	if e, ok := entryMap["vcl2"]; !ok {
		t.Error("vcl2 not found in VCLList")
	} else if want := (adm.VCLEntry{Name: "vcl2", Status: "available", State: "auto", Temperature: adm.VCLTempWarm}); !reflect.DeepEqual(e, want) {
		t.Errorf("vcl2: got %+v, want %+v", e, want)
	}
}

func TestVCLDeps(t *testing.T) {
	t.Parallel()
	v := vtest.New().VclString(baseVCL).AssertStart(t)
	defer v.Stop()
	conn := v.AdmConn()
	ctx := context.Background()

	const vcl2 = "vcl 4.1;\nbackend default none;\nsub vcl_recv { return(synth(200, \"OK\")); }\n"
	if err := conn.VCLInline(ctx, "vcl_dep_test", vcl2, adm.VCLStateAuto); err != nil {
		t.Fatal(err)
	}
	defer conn.VCLDiscard(ctx, "dep_label", "vcl_dep_test")

	if err := conn.VCLLabel(ctx, "dep_label", "vcl_dep_test"); err != nil {
		t.Fatal(err)
	}

	// map["vcl1": [], "dep_label": ["vcl_dep_test"], ...]
	depMap, err := conn.VCLDeps(ctx)
	if err != nil {
		if strings.Contains(err.Error(), "Unknown request") {
			t.Skip("vcl.deps not supported on this varnishd")
		}
		t.Fatal(err)
	}

	if _, ok := depMap["vcl1"]; !ok {
		t.Error("vcl1 not found in VCLDeps")
	}
	// VCLDep{Name: "dep_label", Deps: ["vcl_dep_test"]}
	labelDeps, ok := depMap["dep_label"]
	if !ok {
		t.Error("dep_label not found in VCLDeps")
	} else if !reflect.DeepEqual(labelDeps, []string{"vcl_dep_test"}) {
		t.Errorf("dep_label deps: got %v, want [\"vcl_dep_test\"]", labelDeps)
	}
}

func TestVCLShow(t *testing.T) {
	t.Parallel()
	v := vtest.New().VclString(baseVCL).AssertStart(t)
	defer v.Stop()

	// []VCLFile{{Path: "<vcl.inline>", Content: contains baseVCL}, {Path: "<builtin>", ...}}
	files, err := v.AdmConn().VCLShow(context.Background(), "vcl1")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) < 2 {
		t.Fatalf("VCLShow returned %d files, want at least 2", len(files))
	}
	if files[0].Path != "<vcl.inline>" {
		t.Errorf("files[0].Path: got %q, want \"<vcl.inline>\"", files[0].Path)
	}
	if !strings.Contains(files[0].Content, baseVCL) {
		t.Errorf("files[0].Content does not contain baseVCL:\n%s", files[0].Content)
	}
	if !strings.Contains(strings.ToLower(files[1].Path), "builtin") {
		t.Errorf("files[1].Path: got %q, expected to contain \"builtin\"", files[1].Path)
	}
}

func TestVCLSymtab(t *testing.T) {
	t.Parallel()
	v := vtest.New().VclString(baseVCL).AssertStart(t)
	defer v.Stop()

	sym, err := v.AdmConn().VCLSymtab(context.Background())
	if err != nil {
		if strings.Contains(err.Error(), "Unknown request") {
			t.Skip("vcl.symtab not supported on this varnishd")
		}
		t.Fatal(err)
	}
	if sym == "" {
		t.Error("VCLSymtab returned empty string")
	}
}

func TestVCLInlineUseDiscard(t *testing.T) {
	t.Parallel()
	v := vtest.New().VclString(baseVCL).AssertStart(t)
	defer v.Stop()
	conn := v.AdmConn()
	ctx := context.Background()

	const vcl2 = "vcl 4.1;\nbackend default none;\nsub vcl_recv { return(synth(200, \"vcl2\")); }\n"
	if err := conn.VCLInline(ctx, "vcl2", vcl2, adm.VCLStateAuto); err != nil {
		t.Fatal(err)
	}
	if err := conn.VCLUse(ctx, "vcl2"); err != nil {
		t.Fatal(err)
	}
	if err := conn.VCLUse(ctx, "vcl1"); err != nil {
		t.Fatal(err)
	}
	if err := conn.VCLDiscard(ctx, "vcl2"); err != nil {
		t.Fatal(err)
	}
}

func TestVCLLoadUseDiscard(t *testing.T) {
	t.Parallel()
	v := vtest.New().NoRecordLogs().VclString(baseVCL).AssertStart(t)
	defer v.Stop()
	conn := v.AdmConn()
	ctx := context.Background()

	f := vclTempFile(t)
	if _, err := fmt.Fprintf(f, "vcl 4.1;\nbackend default none;\nsub vcl_recv { return(synth(200, \"loaded\")); }\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()

	if err := conn.VCLLoad(ctx, "vcl_loaded", f.Name(), adm.VCLStateAuto); err != nil {
		t.Fatal(err)
	}
	if err := conn.VCLUse(ctx, "vcl_loaded"); err != nil {
		t.Fatal(err)
	}
	if err := conn.VCLUse(ctx, "vcl1"); err != nil {
		t.Fatal(err)
	}
	if err := conn.VCLDiscard(ctx, "vcl_loaded"); err != nil {
		t.Fatal(err)
	}
}

func TestVCLLabel(t *testing.T) {
	t.Parallel()
	v := vtest.New().NoRecordLogs().VclString(baseVCL).AssertStart(t)
	defer v.Stop()
	conn := v.AdmConn()
	ctx := context.Background()

	f := vclTempFile(t)
	fmt.Fprintf(f, "vcl 4.1;\nbackend default none;\nsub vcl_recv { return(synth(200, \"OK\")); }\n")
	f.Close()

	if err := conn.VCLLoad(ctx, "vcl_to_label", f.Name(), adm.VCLStateAuto); err != nil {
		t.Fatal(err)
	}
	if err := conn.VCLLabel(ctx, "mylabel", "vcl_to_label"); err != nil {
		t.Fatal(err)
	}

	// map["mylabel": {Status: "available", State: "label", Temperature: VCLTempWarm}, "vcl_to_label": {Status: "available", State: "auto", Temperature: VCLTempWarm}]
	entryMap, err := conn.VCLList(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// VCLEntry{Name: "mylabel", Status: "available", State: "label", Temperature: VCLTempWarm, Busy: 0}
	if e, ok := entryMap["mylabel"]; !ok {
		t.Error("mylabel not found in VCLList after VCLLabel")
	} else if want := (adm.VCLEntry{Name: "mylabel", Status: "available", State: "label", Temperature: adm.VCLTempWarm}); !reflect.DeepEqual(e, want) {
		t.Errorf("mylabel: got %+v, want %+v", e, want)
	}
	// VCLEntry{Name: "vcl_to_label", Status: "available", State: "auto", Temperature: VCLTempWarm, Busy: 0}
	if e, ok := entryMap["vcl_to_label"]; !ok {
		t.Error("vcl_to_label not found in VCLList after VCLLabel")
	} else if want := (adm.VCLEntry{Name: "vcl_to_label", Status: "available", State: "auto", Temperature: adm.VCLTempWarm}); !reflect.DeepEqual(e, want) {
		t.Errorf("vcl_to_label: got %+v, want %+v", e, want)
	}

	if err := conn.VCLDiscard(ctx, "mylabel", "vcl_to_label"); err != nil {
		t.Fatal(err)
	}
}

func TestVCLRouting(t *testing.T) {
	t.Parallel()
	v := vtest.New().VclString(baseVCL).AssertStart(t)
	defer v.Stop()
	conn := v.AdmConn()
	ctx := context.Background()

	const vclA = "vcl 4.1;\nbackend default none;\nsub vcl_recv { return(synth(200, \"a\")); }\n"
	if err := conn.VCLInline(ctx, "vcl_a", vclA, adm.VCLStateAuto); err != nil {
		t.Fatal(err)
	}
	if err := conn.VCLLabel(ctx, "label_a", "vcl_a"); err != nil {
		t.Fatal(err)
	}

	const vclB = "vcl 4.1;\nbackend default none;\nsub vcl_recv { return(synth(200, \"b\")); }\n"
	if err := conn.VCLInline(ctx, "vcl_b", vclB, adm.VCLStateAuto); err != nil {
		t.Fatal(err)
	}
	if err := conn.VCLLabel(ctx, "label_b", "vcl_b"); err != nil {
		t.Fatal(err)
	}

	const routingVCL = "vcl 4.1;\nbackend default none;\nsub vcl_recv {\n\tif (req.url ~ \"^/a\") {\n\t\treturn(vcl(label_a));\n\t}\n\treturn(vcl(label_b));\n}\n"
	if err := conn.VCLInline(ctx, "routing_vcl", routingVCL, adm.VCLStateAuto); err != nil {
		t.Fatal(err)
	}
	if err := conn.VCLUse(ctx, "routing_vcl"); err != nil {
		t.Fatal(err)
	}
	defer conn.VCLDiscard(ctx, "vcl_a", "vcl_b")
	defer conn.VCLDiscard(ctx, "label_a", "label_b")
	defer conn.VCLDiscard(ctx, "routing_vcl")
	defer conn.VCLUse(ctx, "vcl1")

	// map["routing_vcl": {Status: "active"}, "label_a": {State: "label"}, "label_b": {State: "label"}, ...]
	entryMap, err := conn.VCLList(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// VCLEntry{Name: "routing_vcl", Status: "active", State: "auto", Temperature: VCLTempWarm, Busy: 0}
	if e, ok := entryMap["routing_vcl"]; !ok {
		t.Error("routing_vcl not found in VCLList")
	} else if want := (adm.VCLEntry{Name: "routing_vcl", Status: "active", State: "auto", Temperature: adm.VCLTempWarm}); !reflect.DeepEqual(e, want) {
		t.Errorf("routing_vcl: got %+v, want %+v", e, want)
	}
	// VCLEntry{Name: "label_a/b", Status: "available", State: "label", Temperature: VCLTempWarm, Busy: 0}
	for _, label := range []string{"label_a", "label_b"} {
		if e, ok := entryMap[label]; !ok {
			t.Errorf("%s not found in VCLList", label)
		} else if want := (adm.VCLEntry{Name: label, Status: "available", State: "label", Temperature: adm.VCLTempWarm}); !reflect.DeepEqual(e, want) {
			t.Errorf("%s: got %+v, want %+v", label, e, want)
		}
	}

	// map["routing_vcl": ["label_a", "label_b"], "label_a": ["vcl_a"], "label_b": ["vcl_b"], ...]
	depMap, err := conn.VCLDeps(ctx)
	if err != nil {
		if strings.Contains(err.Error(), "Unknown request") {
			t.Skip("vcl.deps not supported on this varnishd")
		}
		t.Fatal(err)
	}
	// VCLDep{Name: "routing_vcl", Deps: ["label_a", "label_b"]}
	routingDeps := make(map[string]bool, len(depMap["routing_vcl"]))
	for _, d := range depMap["routing_vcl"] {
		routingDeps[d] = true
	}
	if !routingDeps["label_a"] {
		t.Errorf("label_a not in routing_vcl deps: %v", depMap["routing_vcl"])
	}
	if !routingDeps["label_b"] {
		t.Errorf("label_b not in routing_vcl deps: %v", depMap["routing_vcl"])
	}
	// VCLDep{Name: "label_a", Deps: ["vcl_a"]}, VCLDep{Name: "label_b", Deps: ["vcl_b"]}
	for label, target := range map[string]string{"label_a": "vcl_a", "label_b": "vcl_b"} {
		d, ok := depMap[label]
		if !ok {
			t.Errorf("%s not found in VCLDeps", label)
		} else if !reflect.DeepEqual(d, []string{target}) {
			t.Errorf("%s deps: got %v, want [\"%s\"]", label, d, target)
		}
	}
}

func TestVCLSetState(t *testing.T) {
	t.Parallel()
	v := vtest.New().NoRecordLogs().VclString(baseVCL).AssertStart(t)
	defer v.Stop()
	conn := v.AdmConn()
	ctx := context.Background()

	f := vclTempFile(t)
	fmt.Fprintf(f, "vcl 4.1;\nbackend default none;\nsub vcl_recv { return(synth(200, \"OK\")); }\n")
	f.Close()

	if err := conn.VCLLoad(ctx, "vcl_state_test", f.Name(), adm.VCLStateAuto); err != nil {
		t.Fatal(err)
	}
	defer conn.VCLDiscard(ctx, "vcl_state_test")

	if err := conn.VCLSetState(ctx, "vcl_state_test", adm.VCLStateCold); err != nil {
		t.Fatal(err)
	}
	// map["vcl_state_test": {Status: "available", State: "cold", Temperature: VCLTempCold, Busy: 0}]
	entryMap, err := conn.VCLList(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// VCLEntry{Name: "vcl_state_test", Status: "available", State: "cold", Temperature: VCLTempCold, Busy: 0}
	if e, ok := entryMap["vcl_state_test"]; !ok {
		t.Error("vcl_state_test not found in VCLList")
	} else if want := (adm.VCLEntry{Name: "vcl_state_test", Status: "available", State: "cold", Temperature: adm.VCLTempCold}); !reflect.DeepEqual(e, want) {
		t.Errorf("vcl_state_test: got %+v, want %+v", e, want)
	}
}
