package adm

import (
	"encoding/json"
	"testing"
)

func TestBackendDetailsRawUnmarshal(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name         string
		raw          string
		isEnterprise bool
	}{
		{
			name:         "cache",
			raw:          `{"admin_health":"probe","probe_message":[2,8,"healthy"],"last_change":1780491491.5}`,
			isEnterprise: false,
		},
		{
			name:         "enterprise",
			raw:          `{"admin_health":"probe","probe_health":[2,8,"healthy"],"last_updated":1780491491.5}`,
			isEnterprise: true,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var d backendDetailsRaw
			if err := json.Unmarshal([]byte(tt.raw), &d); err != nil {
				t.Fatal(err)
			}
			var probe *ProbeResult
			var ts float64
			if tt.isEnterprise {
				probe = d.ProbeHealth
				ts = d.LastUpdated
			} else {
				probe = d.ProbeMessage
				ts = d.LastChange
			}
			if probe == nil {
				t.Fatal("probe is nil")
			}
			if probe.Good != 2 || probe.Total != 8 || probe.State != ProbeHealthy {
				t.Errorf("probe: got %+v, want {2 8 ProbeHealthy}", probe)
			}
			if ts == 0 {
				t.Error("timestamp is zero")
			}
		})
	}
}
