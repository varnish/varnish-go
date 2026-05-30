package adm

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// ProbeHealth represents the health state of a backend as reported or set via the admin interface.
type ProbeHealth int

const (
	ProbeUnknown ProbeHealth = iota // unrecognized or unset value; invalid for BackendSetHealth
	ProbeHealthy                    // backend is explicitly set healthy or probe reports healthy
	ProbeSick                       // backend is explicitly set sick or probe reports sick
	ProbeProbe                      // health determined dynamically by a probe (Admin field only); maps to "auto" in BackendSetHealth
)

func probeHealthFromString(s string) ProbeHealth {
	switch s {
	case "healthy":
		return ProbeHealthy
	case "sick":
		return ProbeSick
	case "probe":
		return ProbeProbe
	default:
		return ProbeUnknown
	}
}

// ProbeResult holds the outcome of the most recent health probe for a backend.
type ProbeResult struct {
	Good  int         // number of recent successful probes
	Total int         // total probes in the window
	State ProbeHealth // reported health state
}

func (pr *ProbeResult) UnmarshalJSON(data []byte) error {
	var arr []json.RawMessage
	if err := json.Unmarshal(data, &arr); err != nil {
		return err
	}
	if len(arr) < 3 {
		return fmt.Errorf("probe_message: expected at least 3 elements, got %d", len(arr))
	}
	if err := json.Unmarshal(arr[0], &pr.Good); err != nil {
		return err
	}
	if err := json.Unmarshal(arr[1], &pr.Total); err != nil {
		return err
	}
	var state string
	if err := json.Unmarshal(arr[2], &state); err != nil {
		return err
	}
	pr.State = probeHealthFromString(state)
	return nil
}

// BackendEntry describes a single backend as returned by backend.list.
type BackendEntry struct {
	FullName   string       // full backend name as reported by varnishd, e.g. "vcl1.default"
	VCL        string       // VCL name: FullName up to (excluding) the first dot
	Name       string       // backend name: FullName after the first dot
	Admin      ProbeHealth  // how health state is determined: explicit or by probe
	Probe      *ProbeResult // most recent probe result; nil if no probe is configured
	LastChange time.Time    // when the health state last changed
}

// backendDetailsRaw is the per-backend JSON object in the backend.list response.
type backendDetailsRaw struct {
	AdminHealth string       `json:"admin_health"`
	Probe       *ProbeResult `json:"probe_message"`
	LastChange  float64      `json:"last_change"`
}

// BackendList returns all backends keyed by full name (e.g. "vcl1.default").
// Always issues backend.list -j -p to varnishd (all backends, probe details included).
func (c *Conn) BackendList() (map[string]BackendEntry, error) {
	msg, err := c.Ask("backend.list", "-j", "-p")
	if err != nil {
		return nil, err
	}
	rawMap, err := parseJSONSingle[map[string]backendDetailsRaw](msg)
	if err != nil {
		return nil, err
	}
	result := make(map[string]BackendEntry, len(rawMap))
	for fullName, d := range rawMap {
		sec := int64(d.LastChange)
		e := BackendEntry{
			FullName:   fullName,
			Admin:      probeHealthFromString(d.AdminHealth),
			Probe:      d.Probe,
			LastChange: time.Unix(sec, int64((d.LastChange-float64(sec))*1e9)),
		}
		if i := strings.IndexByte(fullName, '.'); i >= 0 {
			e.VCL = fullName[:i]
			e.Name = fullName[i+1:]
		} else {
			e.VCL = fullName
		}
		result[fullName] = e
	}
	return result, nil
}

var backendPatternRE = regexp.MustCompile(`^[A-Za-z0-9._*]+$`)

// BackendSetHealth sets the health state of all backends matching pattern.
// pattern must contain only [A-Za-z0-9._*] and exactly one dot (e.g. "vcl1.*", "*.default").
// state must not be ProbeUnknown; ProbeProbe maps to "auto" (probe-determined).
func (c *Conn) BackendSetHealth(pattern string, state ProbeHealth) error {
	if !backendPatternRE.MatchString(pattern) || strings.Count(pattern, ".") != 1 {
		return fmt.Errorf("invalid backend pattern %q: must match [A-Za-z0-9._*]+ with exactly one dot", pattern)
	}
	var stateStr string
	switch state {
	case ProbeHealthy:
		stateStr = "healthy"
	case ProbeSick:
		stateStr = "sick"
	case ProbeProbe:
		stateStr = "auto"
	default:
		return fmt.Errorf("invalid health state: %v", state)
	}
	_, err := c.Ask("backend.set_health", pattern, stateStr)
	return err
}
