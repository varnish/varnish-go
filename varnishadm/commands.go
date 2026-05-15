package varnishadm

import (
	"fmt"
	"strconv"
	"time"
)

// Standard Varnish commands

// Ping sends a ping command to varnishd.
func (c *Conn) Ping() (VarnishResponse, error) {
	return c.Exec("ping")
}

// Status returns the status of the Varnish child process.
func (c *Conn) Status() (VarnishResponse, error) {
	return c.Exec("status")
}

// Start starts the Varnish child process.
func (c *Conn) Start() (VarnishResponse, error) {
	return c.Exec("start")
}

// Stop stops the Varnish child process.
func (c *Conn) Stop() (VarnishResponse, error) {
	return c.Exec("stop")
}

// PanicShow shows the panic message if available.
func (c *Conn) PanicShow() (VarnishResponse, error) {
	return c.Exec("panic.show")
}

// PanicClear clears the panic message.
func (c *Conn) PanicClear() (VarnishResponse, error) {
	return c.Exec("panic.clear")
}

// VCL commands

// VCLLoad loads a VCL configuration from a file.
func (c *Conn) VCLLoad(name, path string) (VarnishResponse, error) {
	cmd := fmt.Sprintf("vcl.load %s %s", name, path)
	return c.Exec(cmd)
}

// vclInlineDelimiter is a unique delimiter for vcl.inline heredoc syntax.
// Using a long, unique string avoids collision with VCL content.
const vclInlineDelimiter = "VARNISHADM_END_OF_INLINE_VCL"

// VCLInline loads a VCL configuration from an inline string.
func (c *Conn) VCLInline(name, vcl string) (VarnishResponse, error) {
	// VCL inline uses heredoc-like syntax with << and markers
	cmd := fmt.Sprintf("vcl.inline %s << %s\n%s\n%s", name, vclInlineDelimiter, vcl, vclInlineDelimiter)
	return c.Exec(cmd)
}

// VCLUse switches to using the specified VCL configuration.
func (c *Conn) VCLUse(name string) (VarnishResponse, error) {
	cmd := fmt.Sprintf("vcl.use %s", name)
	return c.Exec(cmd)
}

// VCLLabel assigns a label to a VCL.
func (c *Conn) VCLLabel(label, name string) (VarnishResponse, error) {
	cmd := fmt.Sprintf("vcl.label %s %s", label, name)
	return c.Exec(cmd)
}

// VCLDiscard discards a VCL configuration.
func (c *Conn) VCLDiscard(name string) (VarnishResponse, error) {
	cmd := fmt.Sprintf("vcl.discard %s", name)
	return c.Exec(cmd)
}

// VCLList lists all VCL configurations.
func (c *Conn) VCLList() (VarnishResponse, error) {
	return c.Exec("vcl.list")
}

// VCLListStructured lists all VCL configurations and returns parsed results.
func (c *Conn) VCLListStructured() (*VCLListResult, error) {
	resp, err := c.Exec("vcl.list")
	if err != nil {
		return nil, err
	}
	return parseVCLList(resp.payload)
}

// Parameter commands

// ParamShow shows the value of a parameter.
// If name is empty, shows all parameters.
func (c *Conn) ParamShow(name string) (VarnishResponse, error) {
	if name == "" {
		return c.Exec("param.show")
	}
	cmd := fmt.Sprintf("param.show %s", name)
	return c.Exec(cmd)
}

// ParamSet sets the value of a parameter.
func (c *Conn) ParamSet(name, value string) (VarnishResponse, error) {
	cmd := fmt.Sprintf("param.set %s %s", name, value)
	return c.Exec(cmd)
}

// ParamValue defines acceptable parameter value types.
type ParamValue interface {
	int | bool | float64 | string | time.Duration | Size
}

// ParamSetter is a minimal interface for types that can set parameters.
type ParamSetter interface {
	ParamSet(name, value string) (VarnishResponse, error)
}

// ParamSetTyped sets a parameter with type-safe value conversion.
// Note: This is a package function (not a method) because Go doesn't allow type parameters on methods.
func ParamSetTyped[T ParamValue](v ParamSetter, name string, value T) (VarnishResponse, error) {
	var strValue string

	switch val := any(value).(type) {
	case int:
		strValue = strconv.Itoa(val)
	case bool:
		if val {
			strValue = "on"
		} else {
			strValue = "off"
		}
	case float64:
		strValue = strconv.FormatFloat(val, 'f', -1, 64)
	case string:
		strValue = val
	case time.Duration:
		strValue = fmt.Sprintf("%.0fs", val.Seconds())
	case Size:
		strValue = val.String()
	}

	return v.ParamSet(name, strValue)
}

// Varnish Enterprise TLS commands

// TLSCertList lists all TLS certificates.
func (c *Conn) TLSCertList() (VarnishResponse, error) {
	return c.Exec("tls.cert.list")
}

// TLSCertListStructured lists all TLS certificates and returns parsed results.
func (c *Conn) TLSCertListStructured() (*TLSCertListResult, error) {
	resp, err := c.Exec("tls.cert.list")
	if err != nil {
		return nil, err
	}
	return parseTLSCertList(resp.payload)
}

// TLSCertLoad loads a TLS certificate and key file.
// If privateKeyFile is empty, certFile is expected to contain both cert and key.
func (c *Conn) TLSCertLoad(name, certFile, privateKeyFile string) (VarnishResponse, error) {
	var cmd string
	if privateKeyFile == "" {
		cmd = fmt.Sprintf("tls.cert.load %s %s", name, certFile)
	} else {
		cmd = fmt.Sprintf("tls.cert.load %s %s -k %s", name, certFile, privateKeyFile)
	}
	return c.Exec(cmd)
}

// TLSCertDiscard discards a TLS certificate by ID.
func (c *Conn) TLSCertDiscard(id string) (VarnishResponse, error) {
	cmd := fmt.Sprintf("tls.cert.discard %s", id)
	return c.Exec(cmd)
}

// TLSCertCommit commits the loaded TLS certificates.
func (c *Conn) TLSCertCommit() (VarnishResponse, error) {
	return c.Exec("tls.cert.commit")
}

// TLSCertRollback rolls back the TLS certificate changes.
func (c *Conn) TLSCertRollback() (VarnishResponse, error) {
	return c.Exec("tls.cert.rollback")
}

// TLSCertReload reloads all TLS certificates.
func (c *Conn) TLSCertReload() (VarnishResponse, error) {
	return c.Exec("tls.cert.reload")
}
