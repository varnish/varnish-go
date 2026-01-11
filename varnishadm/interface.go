package varnishadm

// Commander is the interface for executing Varnish CLI commands.
// Both *Conn and *MockVarnishadm implement this interface.
type Commander interface {
	// Exec executes a raw command and returns the response.
	Exec(cmd string) (VarnishResponse, error)

	// Close closes the connection.
	Close() error

	// Standard commands
	Ping() (VarnishResponse, error)
	Status() (VarnishResponse, error)
	Start() (VarnishResponse, error)
	Stop() (VarnishResponse, error)
	PanicShow() (VarnishResponse, error)
	PanicClear() (VarnishResponse, error)

	// VCL commands
	VCLLoad(name, path string) (VarnishResponse, error)
	VCLInline(name, vcl string) (VarnishResponse, error)
	VCLUse(name string) (VarnishResponse, error)
	VCLLabel(label, name string) (VarnishResponse, error)
	VCLDiscard(name string) (VarnishResponse, error)
	VCLList() (VarnishResponse, error)
	VCLListStructured() (*VCLListResult, error)

	// Parameter commands
	ParamShow(name string) (VarnishResponse, error)
	ParamSet(name, value string) (VarnishResponse, error)

	// Varnish Enterprise TLS commands
	TLSCertList() (VarnishResponse, error)
	TLSCertListStructured() (*TLSCertListResult, error)
	TLSCertLoad(name, certFile, privateKeyFile string) (VarnishResponse, error)
	TLSCertCommit() (VarnishResponse, error)
	TLSCertRollback() (VarnishResponse, error)
	TLSCertReload() (VarnishResponse, error)
	TLSCertDiscard(id string) (VarnishResponse, error)
}

// Ensure *Conn implements Commander
var _ Commander = (*Conn)(nil)

// VarnishadmInterface is the legacy interface name.
// Deprecated: Use Commander instead.
type VarnishadmInterface = Commander
