package varnish

import (
	"fmt"
	"net"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

var udsModeRE = regexp.MustCompile(`^[0-7]{3}$`)

// httpsListener records an HTTPS listener's certificate files, loaded and
// committed once VarnishBuilder.Build succeeds.
type httpsListener struct {
	name     string
	pemFiles []string
}

// registerListenerName records name as used by a listener, returning an
// error if it (or any other HTTP/HTTPS/UDS listener) already used it.
func (vb *VarnishBuilder) registerListenerName(name string) error {
	if vb.listenerNames == nil {
		vb.listenerNames = make(map[string]struct{})
	}
	if _, ok := vb.listenerNames[name]; ok {
		return fmt.Errorf("varnish: listener name %q already used", name)
	}
	vb.listenerNames[name] = struct{}{}
	return nil
}

// setBuildErr records err as the build error, if one isn't already recorded.
func (vb *VarnishBuilder) setBuildErr(err error) {
	if vb.buildErr == nil {
		vb.buildErr = err
	}
}

// validateSocket checks that socket is ":<port>", "<ipv4>:<port>", or
// "[<ipv6>]:<port>".
func validateSocket(socket string) error {
	host, port, err := net.SplitHostPort(socket)
	if err != nil {
		return fmt.Errorf("invalid socket %q: %w", socket, err)
	}
	if port == "" {
		return fmt.Errorf("invalid socket %q: missing port", socket)
	}
	if _, err := strconv.Atoi(port); err != nil {
		return fmt.Errorf("invalid socket %q: invalid port %q", socket, port)
	}
	if host != "" && net.ParseIP(host) == nil {
		return fmt.Errorf("invalid socket %q: host must be empty or a valid IP address", socket)
	}
	return nil
}

// HTTPListener adds a named plain-HTTP listener. socket must be ":<port>",
// "<ipv4>:<port>", or "[<ipv6>]:<port>". name must be unique across all
// listeners added via [VarnishBuilder.HTTPListener], [VarnishBuilder.HTTPSListener],
// and [VarnishBuilder.UDSSocket].
func (vb *VarnishBuilder) HTTPListener(name, socket string) *VarnishBuilder {
	if err := vb.registerListenerName(name); err != nil {
		vb.setBuildErr(err)
		return vb
	}
	if err := validateSocket(socket); err != nil {
		vb.setBuildErr(fmt.Errorf("varnish: HTTPListener %q: %w", name, err))
		return vb
	}
	vb.addresses = append(vb.addresses, name+"="+socket)
	return vb
}

// HTTPSListener adds a named HTTPS listener. socket follows the same rules
// as [VarnishBuilder.HTTPListener]. pemFiles are self-contained
// certificate+key PEM files, loaded via tls.cert.load and committed via
// tls.cert.commit automatically once [VarnishBuilder.Build] succeeds. For a
// separate cert/key pair, combine them into one file first (the vtest
// package's PEMFile does this for you).
func (vb *VarnishBuilder) HTTPSListener(name, socket string, pemFiles ...string) *VarnishBuilder {
	if err := vb.registerListenerName(name); err != nil {
		vb.setBuildErr(err)
		return vb
	}
	if err := validateSocket(socket); err != nil {
		vb.setBuildErr(fmt.Errorf("varnish: HTTPSListener %q: %w", name, err))
		return vb
	}
	vb.addresses = append(vb.addresses, name+"="+socket+","+tlsProto())
	vb.httpsListeners = append(vb.httpsListeners, httpsListener{name: name, pemFiles: pemFiles})
	return vb
}

// UDSOptions configures optional Unix domain socket permissions for
// [VarnishBuilder.UDSSocket]. Use [NewUDSOptions] to build one.
type UDSOptions struct {
	user  string
	group string
	mode  string
}

// NewUDSOptions creates an empty [UDSOptions] ready for chaining.
func NewUDSOptions() *UDSOptions {
	return &UDSOptions{}
}

// User sets the owning user of the socket file. Ignored for abstract sockets.
func (o *UDSOptions) User(user string) *UDSOptions {
	o.user = user
	return o
}

// Group sets the owning group of the socket file. Ignored for abstract sockets.
func (o *UDSOptions) Group(group string) *UDSOptions {
	o.group = group
	return o
}

// Mode sets the socket file's permissions as a 3-digit octal value (e.g.
// "660"). Ignored for abstract sockets.
func (o *UDSOptions) Mode(mode string) *UDSOptions {
	o.mode = mode
	return o
}

// UDSSocket adds a named Unix domain socket listener. path must be an
// absolute path ("/path/to/listen.sock") or "@" followed by an abstract
// socket name ("@myvarnishd"). name must be unique across all listeners
// added via [VarnishBuilder.HTTPListener], [VarnishBuilder.HTTPSListener],
// and UDSSocket. opts may be nil.
func (vb *VarnishBuilder) UDSSocket(name, path string, opts *UDSOptions) *VarnishBuilder {
	if err := vb.registerListenerName(name); err != nil {
		vb.setBuildErr(err)
		return vb
	}
	if !filepath.IsAbs(path) && !strings.HasPrefix(path, "@") {
		vb.setBuildErr(fmt.Errorf("varnish: UDSSocket %q: path must be absolute or start with \"@\", got %q", name, path))
		return vb
	}

	if opts == nil {
		opts = &UDSOptions{}
	}
	if opts.mode != "" && !udsModeRE.MatchString(opts.mode) {
		vb.setBuildErr(fmt.Errorf("varnish: UDSSocket %q: mode must be a 3-digit octal value, got %q", name, opts.mode))
		return vb
	}

	arg := name + "=" + path
	if opts.user != "" {
		arg += ",user=" + opts.user
	}
	if opts.group != "" {
		arg += ",group=" + opts.group
	}
	if opts.mode != "" {
		arg += ",mode=" + opts.mode
	}
	vb.addresses = append(vb.addresses, arg)
	return vb
}
