package adm

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// VCLState is the desired cold/warm/auto state for VCL load and state commands.
// The zero value VCLStateAuto is the varnishd default.
type VCLState int

const (
	VCLStateAuto VCLState = iota // let varnishd manage temperature automatically
	VCLStateCold                 // keep VCL compiled but not serving traffic
	VCLStateWarm                 // keep VCL active and ready to serve
)

func (s VCLState) String() string {
	switch s {
	case VCLStateCold:
		return "cold"
	case VCLStateWarm:
		return "warm"
	default:
		return "auto"
	}
}

// VCLTemperature is the current runtime temperature of a loaded VCL as reported by varnishd.
type VCLTemperature int

const (
	VCLTempUnknown VCLTemperature = iota // unrecognized value
	VCLTempInit                          // VCL is being initialized
	VCLTempCold                          // VCL is compiled but not serving
	VCLTempWarm                          // VCL is active and serving traffic
	VCLTempBusy                          // VCL is warming up
	VCLTempCooling                       // VCL is transitioning to cold
)

func (t VCLTemperature) String() string {
	switch t {
	case VCLTempInit:
		return "init"
	case VCLTempCold:
		return "cold"
	case VCLTempWarm:
		return "warm"
	case VCLTempBusy:
		return "busy"
	case VCLTempCooling:
		return "cooling"
	default:
		return "unknown"
	}
}

func vclTempFromString(s string) VCLTemperature {
	switch s {
	case "init":
		return VCLTempInit
	case "cold":
		return VCLTempCold
	case "warm":
		return VCLTempWarm
	case "busy":
		return VCLTempBusy
	case "cooling":
		return VCLTempCooling
	default:
		return VCLTempUnknown
	}
}

// VCLEntry describes a single loaded VCL configuration or label.
// Temperature is parsed from the JSON "temperature" string.
type VCLEntry struct {
	Status      string         `json:"status"` // "active", "available", or "discarded"
	State       string         `json:"state"`  // "label", "cold", "warm", or "auto"
	Temperature VCLTemperature // current runtime temperature
	Busy        int            `json:"busy"` // number of active references
	Name        string         `json:"name"` // configuration name or label
}

type vclEntryRaw struct {
	Status      string `json:"status"`
	State       string `json:"state"`
	Temperature string `json:"temperature"`
	Busy        int    `json:"busy"`
	Name        string `json:"name"`
}

func (e *VCLEntry) UnmarshalJSON(data []byte) error {
	var raw vclEntryRaw
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	e.Status = raw.Status
	e.State = raw.State
	e.Temperature = vclTempFromString(raw.Temperature)
	e.Busy = raw.Busy
	e.Name = raw.Name
	return nil
}

// VCLList returns all loaded VCL configurations and labels, keyed by name.
func (c *Conn) VCLList(ctx context.Context) (map[string]VCLEntry, error) {
	msg, err := c.Ask(ctx, "vcl.list", "-j")
	if err != nil {
		return nil, err
	}
	items, err := parseJSONItems[VCLEntry](msg)
	if err != nil {
		return nil, err
	}
	m := make(map[string]VCLEntry, len(items))
	for _, e := range items {
		m[e.Name] = e
	}
	return m, nil
}

type vclDepRaw struct {
	Name string   `json:"name"`
	Deps []string `json:"deps"`
}

// VCLDeps returns dependency information for all loaded VCL configurations,
// keyed by VCL name with the list of names it directly depends on as the value.
func (c *Conn) VCLDeps(ctx context.Context) (map[string][]string, error) {
	msg, err := c.Ask(ctx, "vcl.deps", "-j")
	if err != nil {
		return nil, err
	}
	items, err := parseJSONItems[vclDepRaw](msg)
	if err != nil {
		return nil, err
	}
	m := make(map[string][]string, len(items))
	for _, d := range items {
		m[d.Name] = d.Deps
	}
	return m, nil
}

// VCLLoad compiles and loads the VCL file at path under the given name.
// state controls the initial temperature; VCLStateAuto lets varnishd decide.
func (c *Conn) VCLLoad(ctx context.Context, name, file string, state VCLState) error {
	_, err := c.Ask(ctx, "vcl.load", name, file, state.String())
	return err
}

// VCLInline compiles and loads VCL source inline under the given name.
// state controls the initial temperature; VCLStateAuto lets varnishd decide.
// Uses heredoc syntax to support multi-line VCL content.
func (c *Conn) VCLInline(ctx context.Context, name, vcl string, state VCLState) error {
	h := sha256.Sum256([]byte(vcl))
	marker := fmt.Sprintf("HEREDOC_%X", h[:8])
	args := []string{"vcl.inline", name + " << " + marker + "\n", vcl, "\n" + marker}
	if state != VCLStateAuto {
		args = append(args, state.String())
	}
	_, err := c.Ask(ctx, args...)
	return err
}

// VCLUse switches the active VCL to the named configuration or label.
func (c *Conn) VCLUse(ctx context.Context, name string) error {
	_, err := c.Ask(ctx, "vcl.use", name)
	return err
}

// VCLDiscard unloads named configurations and labels. Each name is discarded
// individually; if any fails, the remaining names are not attempted.
func (c *Conn) VCLDiscard(ctx context.Context, names ...string) error {
	for _, name := range names {
		if _, err := c.Ask(ctx, "vcl.discard", name); err != nil {
			return err
		}
	}
	return nil
}

// VCLLabel applies a symbolic label to a named VCL configuration.
func (c *Conn) VCLLabel(ctx context.Context, label, configname string) error {
	_, err := c.Ask(ctx, "vcl.label", label, configname)
	return err
}

// VCLSetState forces the temperature state of a named VCL configuration.
func (c *Conn) VCLSetState(ctx context.Context, name string, state VCLState) error {
	_, err := c.Ask(ctx, "vcl.state", name, state.String())
	return err
}

// VCLFile describes a single source file making up a VCL configuration.
// Each VCL may expand into multiple files when it includes other files.
type VCLFile struct {
	Path    string // file path as reported by varnishd, e.g. "/etc/vcl/main.vcl" or "<builtin>"
	Content string // full source content of the file
}

// VCLShow returns the source files of the named VCL configuration.
// If name is empty, shows the active VCL.
// Returns one VCLFile per source file (main VCL and all includes) in encounter order.
func (c *Conn) VCLShow(ctx context.Context, name string) ([]VCLFile, error) {
	args := []string{"vcl.show", "-v"}
	if name != "" {
		args = append(args, name)
	}
	src, err := c.Ask(ctx, args...)
	if err != nil {
		return nil, err
	}
	return parseVCLShow(src)
}

// parseVCLShow splits the output of vcl.show -v into individual VCLFile values.
// Each file is introduced by a "// VCL.SHOW <index> <byte_count> <path>" marker line
// followed by exactly byte_count bytes of VCL source.
func parseVCLShow(s string) ([]VCLFile, error) {
	const prefix = "// VCL.SHOW "
	var files []VCLFile
	for {
		i := strings.Index(s, prefix)
		if i < 0 {
			break
		}
		s = s[i+len(prefix):]
		nl := strings.IndexByte(s, '\n')
		if nl < 0 {
			return nil, fmt.Errorf("unterminated VCL.SHOW marker")
		}
		markerLine := s[:nl]
		fields := strings.Fields(markerLine)
		s = s[nl+1:]
		if len(fields) < 3 {
			return nil, fmt.Errorf("malformed VCL.SHOW marker: %q", markerLine)
		}
		n, err := strconv.Atoi(fields[1])
		if err != nil || n < 0 || n > len(s) {
			return nil, fmt.Errorf("invalid byte count in VCL.SHOW: %q", fields[1])
		}
		files = append(files, VCLFile{Path: fields[2], Content: s[:n]})
		s = s[n:]
	}
	return files, nil
}

// VCLSymtab dumps the VCL symbol tables for debugging.
func (c *Conn) VCLSymtab(ctx context.Context) (string, error) {
	return c.Ask(ctx, "vcl.symtab")
}
