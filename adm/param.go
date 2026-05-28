package adm

import "fmt"

// ParamInfo describes a single varnishd runtime parameter and its current value.
type ParamInfo struct {
	Name        string   `json:"name"`                  // parameter name
	Implemented bool     `json:"implemented"`           // false if not supported on this platform
	Value       any      `json:"value,omitempty"`       // current value (string or number depending on parameter type)
	Default     string   `json:"default,omitempty"`     // compiled-in default
	Units       string   `json:"units,omitempty"`       // unit of measurement, if any
	Minimum     string   `json:"minimum,omitempty"`     // minimum allowed value, if bounded
	Maximum     string   `json:"maximum,omitempty"`     // maximum allowed value, if bounded
	Description string   `json:"description,omitempty"` // human-readable description
	Flags       []string `json:"flags,omitempty"`       // e.g. "delayed_effect", "experimental"
}

// ParamShow returns all varnishd runtime parameters.
func (c *Conn) ParamShow() (map[string]ParamInfo, error) {
	return paramShow(c, false)
}

// ParamShowChanged returns only parameters whose values differ from their compiled-in defaults.
func (c *Conn) ParamShowChanged() (map[string]ParamInfo, error) {
	return paramShow(c, true)
}

func paramShow(c *Conn, changed bool) (map[string]ParamInfo, error) {
	args := []string{"param.show", "-j"}
	if changed {
		args = append(args, "changed")
	}
	msg, err := c.Ask(args...)
	if err != nil {
		return nil, err
	}
	items, err := parseJSONItems[ParamInfo](msg)
	if err != nil {
		return nil, err
	}
	result := make(map[string]ParamInfo, len(items))
	for _, p := range items {
		result[p.Name] = p
	}
	return result, nil
}

// ParamSet sets a runtime parameter to value and returns its updated info.
func (c *Conn) ParamSet(param, value string) (ParamInfo, error) {
	msg, err := c.Ask("param.set", "-j", param, value)
	if err != nil {
		return ParamInfo{}, err
	}
	return parseJSONSingle[ParamInfo](msg)
}

// ParamReset resets a runtime parameter to its compiled-in default and returns its updated info.
func (c *Conn) ParamReset(param string) (ParamInfo, error) {
	status, msg, err := c.AskRaw("param.reset", "-j", param)
	if err != nil {
		return ParamInfo{}, err
	}
	if status == 102 {
		// Some varnishd editions return 102 "JSON unimplemented" for param.reset -j.
		// Fall back to plain text and return minimal info.
		if _, err := c.Ask("param.reset", param); err != nil {
			return ParamInfo{}, err
		}
		return ParamInfo{Name: param}, nil
	}
	if status != 200 {
		return ParamInfo{}, fmt.Errorf("command: param.reset -j %s\nfailed with %d status and message message:\n%s", param, status, string(msg))
	}
	return parseJSONSingle[ParamInfo](string(msg))
}
