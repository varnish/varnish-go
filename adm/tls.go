package adm

import (
	"encoding/json"
	"fmt"
	"strings"
)

// TLSCertEntry describes a single TLS certificate binding as reported by tls.cert.list.
// The same certificate ID appears once per frontend it is bound to.
type TLSCertEntry struct {
	Frontend string `json:"frontend"` // listener name, e.g. "HTTPS"
	ID       string `json:"id"`       // certificate identifier, e.g. "cert0"
	Status   string `json:"status"`   // "active", "staged", or "discard"
	Subject  string `json:"subject"`  // certificate subject CN
}

// TLSCertList returns all currently loaded TLS certificate bindings.
func (c *Conn) TLSCertList() ([]TLSCertEntry, error) {
	msg, err := c.Ask("tls.cert.list", "-j")
	if err != nil {
		return nil, err
	}
	var entries []TLSCertEntry
	if err := json.Unmarshal([]byte(msg), &entries); err != nil {
		return nil, fmt.Errorf("parse tls.cert.list: %w", err)
	}
	return entries, nil
}

// TLSCertDiscard marks the certificate with the given ID for removal from all frontends.
func (c *Conn) TLSCertDiscard(id string) error {
	_, err := c.Ask("tls.cert.discard", id)
	return err
}

type tlsCertArgs struct {
	id           string
	frontend     string
	keyFile      string
	protocols    *string
	ciphers      *string
	cipherSuites *string
	defaultCert  bool
	serverOrder  bool
}

// TLSOption configures an optional parameter for TLSCertLoad.
type TLSOption func(*tlsCertArgs) error

// TLSWithCertID sets the optional cert ID (positional, before the filename).
// The id must not be empty or contain spaces.
func TLSWithCertID(id string) TLSOption {
	return func(a *tlsCertArgs) error {
		if id == "" {
			return fmt.Errorf("TLSWithCertID: id must not be empty")
		}
		if strings.ContainsRune(id, ' ') {
			return fmt.Errorf("TLSWithCertID: %q must not contain spaces", id)
		}
		a.id = id
		return nil
	}
}

// TLSWithFrontend binds the certificate to a specific frontend (-f).
// The name must not be empty or contain spaces.
func TLSWithFrontend(name string) TLSOption {
	return func(a *tlsCertArgs) error {
		if name == "" {
			return fmt.Errorf("TLSWithFrontend: name must not be empty")
		}
		if strings.ContainsRune(name, ' ') {
			return fmt.Errorf("TLSWithFrontend: %q must not contain spaces", name)
		}
		a.frontend = name
		return nil
	}
}

// TLSWithKeyFile specifies a separate private key file (-k).
// The path must not be empty.
func TLSWithKeyFile(path string) TLSOption {
	return func(a *tlsCertArgs) error {
		if path == "" {
			return fmt.Errorf("TLSWithKeyFile: path must not be empty")
		}
		a.keyFile = path
		return nil
	}
}

// TLSWithProtocols sets the SSL/TLS protocols (-p), e.g. TLSWithProtocols("TLSv1.2", "TLSv1.3").
// At least one protocol must be provided. Elements are joined with ',' (the protocol-list separator);
// each must not contain spaces or commas.
func TLSWithProtocols(protos ...string) TLSOption {
	return func(a *tlsCertArgs) error {
		if len(protos) == 0 {
			return fmt.Errorf("TLSWithProtocols: at least one protocol must be provided")
		}
		for _, p := range protos {
			if strings.ContainsAny(p, " ,") {
				return fmt.Errorf("TLSWithProtocols: %q must not contain spaces or commas", p)
			}
		}
		s := strings.Join(protos, ",")
		a.protocols = &s
		return nil
	}
}

// TLSWithCiphers sets the TLS 1.2 (and earlier) cipher list (-c).
// At least one cipher must be provided. Elements are joined with ':' (the OpenSSL cipher-list separator);
// each must not contain spaces or colons.
func TLSWithCiphers(ciphers ...string) TLSOption {
	return func(a *tlsCertArgs) error {
		if len(ciphers) == 0 {
			return fmt.Errorf("TLSWithCiphers: at least one cipher must be provided")
		}
		for _, c := range ciphers {
			if strings.ContainsAny(c, " :") {
				return fmt.Errorf("TLSWithCiphers: %q must not contain spaces or colons", c)
			}
		}
		s := strings.Join(ciphers, ":")
		a.ciphers = &s
		return nil
	}
}

// TLSWithCipherSuites sets the TLS 1.3 ciphersuites (-s).
// At least one suite must be provided. Elements are joined with ':' (the OpenSSL ciphersuite separator);
// each must not contain spaces or colons.
func TLSWithCipherSuites(suites ...string) TLSOption {
	return func(a *tlsCertArgs) error {
		if len(suites) == 0 {
			return fmt.Errorf("TLSWithCipherSuites: at least one suite must be provided")
		}
		for _, suite := range suites {
			if strings.ContainsAny(suite, " :") {
				return fmt.Errorf("TLSWithCipherSuites: %q must not contain spaces or colons", suite)
			}
		}
		s := strings.Join(suites, ":")
		a.cipherSuites = &s
		return nil
	}
}

// TLSWithDefaultCert marks the certificate as the default fallback (-d).
func TLSWithDefaultCert() TLSOption {
	return func(a *tlsCertArgs) error {
		a.defaultCert = true
		return nil
	}
}

// TLSWithServerCipherOrder prefers the server's cipher order over the client's (-o).
func TLSWithServerCipherOrder() TLSOption {
	return func(a *tlsCertArgs) error {
		a.serverOrder = true
		return nil
	}
}

// TLSCertLoad stages a TLS certificate from certFile for commit.
func (c *Conn) TLSCertLoad(certFile string, opts ...TLSOption) error {
	var a tlsCertArgs
	for _, o := range opts {
		if err := o(&a); err != nil {
			return err
		}
	}
	args := []string{"tls.cert.load"}
	if a.id != "" {
		args = append(args, a.id)
	}
	args = append(args, certFile)
	if a.frontend != "" {
		args = append(args, "-f", a.frontend)
	}
	if a.keyFile != "" {
		args = append(args, "-k", a.keyFile)
	}
	if a.protocols != nil {
		args = append(args, "-p", *a.protocols)
	}
	if a.ciphers != nil {
		args = append(args, "-c", *a.ciphers)
	}
	if a.cipherSuites != nil {
		args = append(args, "-s", *a.cipherSuites)
	}
	if a.defaultCert {
		args = append(args, "-d")
	}
	if a.serverOrder {
		args = append(args, "-o")
	}
	_, err := c.Ask(args...)
	return err
}

// TLSCertCommit applies all staged TLS certificate changes.
func (c *Conn) TLSCertCommit() error {
	_, err := c.Ask("tls.cert.commit")
	return err
}

// TLSCertRollback discards all staged TLS certificate changes.
func (c *Conn) TLSCertRollback() error {
	_, err := c.Ask("tls.cert.rollback")
	return err
}
