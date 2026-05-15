package varnishadm

// Callbacks contains optional callback functions for connection events.
// All callbacks are optional - nil callbacks are ignored.
type Callbacks struct {
	// OnConnect is called when a connection is established and authenticated.
	// For server mode: called when varnishd connects to us.
	// For client mode: called after we connect and authenticate to varnishd.
	OnConnect func(conn *Conn)

	// OnDisconnect is called when a connection is closed.
	// err is nil for clean closes, non-nil for errors.
	OnDisconnect func(conn *Conn, err error)

	// OnAuthFail is called when authentication fails.
	// remoteAddr is the address of the remote end.
	OnAuthFail func(remoteAddr string, err error)

	// OnError is called on protocol/communication errors during operation.
	// This is called for errors after the connection is established.
	OnError func(conn *Conn, err error)
}

// invoke safely calls a callback if it's not nil.
func (c *Callbacks) invokeConnect(conn *Conn) {
	if c != nil && c.OnConnect != nil {
		c.OnConnect(conn)
	}
}

func (c *Callbacks) invokeDisconnect(conn *Conn, err error) {
	if c != nil && c.OnDisconnect != nil {
		c.OnDisconnect(conn, err)
	}
}

func (c *Callbacks) invokeAuthFail(remoteAddr string, err error) {
	if c != nil && c.OnAuthFail != nil {
		c.OnAuthFail(remoteAddr, err)
	}
}

func (c *Callbacks) invokeError(conn *Conn, err error) {
	if c != nil && c.OnError != nil {
		c.OnError(conn, err)
	}
}
