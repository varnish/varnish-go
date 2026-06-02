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
	msg = strings.TrimSpace(msg)
	if msg == "" {
		return nil, nil
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

// TLSCertLoad stages a TLS certificate from certFile for commit.
// keyFile is an optional separate private key file; pass "" if the key is embedded in certFile.
func (c *Conn) TLSCertLoad(certFile, keyFile string) error {
	args := []string{"tls.cert.load", certFile}
	if keyFile != "" {
		args = append(args, "-k", keyFile)
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
