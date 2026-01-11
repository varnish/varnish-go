package varnishadm

import (
	"errors"
	"testing"
)

func TestCallbacks_NilSafe(t *testing.T) {
	// nil Callbacks should not panic
	var cb *Callbacks
	cb.invokeConnect(nil)
	cb.invokeDisconnect(nil, nil)
	cb.invokeAuthFail("", nil)
	cb.invokeError(nil, nil)
}

func TestCallbacks_EmptyStruct(t *testing.T) {
	// Empty Callbacks (all nil funcs) should not panic
	cb := &Callbacks{}
	cb.invokeConnect(nil)
	cb.invokeDisconnect(nil, nil)
	cb.invokeAuthFail("", nil)
	cb.invokeError(nil, nil)
}

func TestCallbacks_OnConnect(t *testing.T) {
	var called bool
	var gotConn *Conn

	cb := &Callbacks{
		OnConnect: func(c *Conn) {
			called = true
			gotConn = c
		},
	}

	// Pass a nil conn for simplicity
	cb.invokeConnect(nil)

	if !called {
		t.Error("OnConnect not called")
	}
	if gotConn != nil {
		t.Error("expected nil conn")
	}
}

func TestCallbacks_OnDisconnect(t *testing.T) {
	var called bool
	var gotConn *Conn
	var gotErr error

	cb := &Callbacks{
		OnDisconnect: func(c *Conn, err error) {
			called = true
			gotConn = c
			gotErr = err
		},
	}

	testErr := errors.New("test error")
	cb.invokeDisconnect(nil, testErr)

	if !called {
		t.Error("OnDisconnect not called")
	}
	if gotConn != nil {
		t.Error("expected nil conn")
	}
	if gotErr != testErr {
		t.Errorf("got err %v, want %v", gotErr, testErr)
	}
}

func TestCallbacks_OnAuthFail(t *testing.T) {
	var called bool
	var gotAddr string
	var gotErr error

	cb := &Callbacks{
		OnAuthFail: func(addr string, err error) {
			called = true
			gotAddr = addr
			gotErr = err
		},
	}

	testErr := errors.New("auth failed")
	cb.invokeAuthFail("127.0.0.1:9999", testErr)

	if !called {
		t.Error("OnAuthFail not called")
	}
	if gotAddr != "127.0.0.1:9999" {
		t.Errorf("got addr %q, want %q", gotAddr, "127.0.0.1:9999")
	}
	if gotErr != testErr {
		t.Errorf("got err %v, want %v", gotErr, testErr)
	}
}

func TestCallbacks_OnError(t *testing.T) {
	var called bool
	var gotConn *Conn
	var gotErr error

	cb := &Callbacks{
		OnError: func(c *Conn, err error) {
			called = true
			gotConn = c
			gotErr = err
		},
	}

	testErr := errors.New("protocol error")
	cb.invokeError(nil, testErr)

	if !called {
		t.Error("OnError not called")
	}
	if gotConn != nil {
		t.Error("expected nil conn")
	}
	if gotErr != testErr {
		t.Errorf("got err %v, want %v", gotErr, testErr)
	}
}
