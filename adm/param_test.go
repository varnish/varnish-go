package adm_test

import (
	"context"
	"testing"

	"github.com/varnish/varnish-go/vtest"
)

func TestParamShow(t *testing.T) {
	t.Parallel()
	v := vtest.New().VclString(baseVCL).AssertStart(t)
	defer v.Stop()

	params, err := v.AdmConn().ParamShow(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(params) == 0 {
		t.Fatal("expected non-empty param list")
	}
	if _, ok := params["workspace_client"]; !ok {
		t.Error("workspace_client not found in params")
	}
}

func TestParamShowChanged(t *testing.T) {
	t.Parallel()
	v := vtest.New().VclString(baseVCL).AssertStart(t)
	defer v.Stop()

	// vtest may have changed some params; just verify the call succeeds
	_, err := v.AdmConn().ParamShowChanged(context.Background())
	if err != nil {
		t.Fatal(err)
	}
}

func TestParamSetReset(t *testing.T) {
	t.Parallel()
	v := vtest.New().VclString(baseVCL).AssertStart(t)
	defer v.Stop()
	conn := v.AdmConn()
	ctx := context.Background()

	// ParamInfo{Name: "workspace_client", Value: "128k"}
	updated, err := conn.ParamSet(ctx, "workspace_client", "128k")
	if err != nil {
		t.Fatal(err)
	}
	if updated.Name != "workspace_client" {
		t.Errorf("ParamSet: got name %q, want \"workspace_client\"", updated.Name)
	}

	// ParamInfo{Name: "workspace_client", Value: <default>}
	reset, err := conn.ParamReset(ctx, "workspace_client")
	if err != nil {
		t.Fatal(err)
	}
	if reset.Name != "workspace_client" {
		t.Errorf("ParamReset: got name %q, want \"workspace_client\"", reset.Name)
	}
}
