package adm_test

import (
	"context"
	"testing"

	"github.com/varnish/varnish-go/vtest"
)

func TestBanListBan(t *testing.T) {
	t.Parallel()
	v := vtest.New().VclString(baseVCL).AssertStart(t)
	defer v.Stop()
	conn := v.AdmConn()
	ctx := context.Background()

	if err := conn.Ban(ctx, "req.url ~ /test"); err != nil {
		t.Fatal(err)
	}
	bans, err := conn.BanList(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// BanEntry{Spec: "req.url ~ /test", Time: non-zero}
	var found bool
	for _, b := range bans {
		if b.Spec == "req.url ~ /test" {
			found = true
			if b.Time.IsZero() {
				t.Error("BanEntry.Time is zero")
			}
		}
	}
	if !found {
		t.Error("created ban not found in BanList")
	}
}
