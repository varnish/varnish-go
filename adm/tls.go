package adm

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
