# varnishadm

Go library for communicating with Varnish Cache via the CLI protocol.

## Modes

**Client mode** - connect to a running varnishd:
```go
conn, err := varnishadm.Connect("varnishd")
// or with explicit endpoint:
conn, err := varnishadm.ConnectRaw("localhost:6082", "/etc/varnish/secret")
```

**Server mode** - accept connections from varnishd started with `-M`:
```go
listener, _ := net.Listen("tcp", ":9999")
server := varnishadm.NewServer(listener, "secret")
conn, err := server.Accept(ctx)
```

## Commands

```go
conn.Ping()
conn.Status()
conn.Start()
conn.Stop()

conn.VCLLoad("name", "/path/to/file.vcl")
conn.VCLInline("name", "vcl 4.1; backend default { .host = \"localhost\"; }")
conn.VCLUse("name")
conn.VCLList()
conn.VCLDiscard("name")

conn.ParamShow("default_ttl")
conn.ParamSet("default_ttl", "120")

// Enterprise only
conn.TLSCertLoad("cert1", "/path/to/cert.pem", "")
conn.TLSCertCommit()
```

## Callbacks

```go
server := varnishadm.NewServer(listener, secret,
    varnishadm.WithServerCallbacks(&varnishadm.Callbacks{
        OnConnect:    func(c *varnishadm.Conn) { ... },
        OnDisconnect: func(c *varnishadm.Conn, err error) { ... },
        OnAuthFail:   func(addr string, err error) { ... },
        OnError:      func(c *varnishadm.Conn, err error) { ... },
    }),
)
```

## Testing

Use `MockVarnishadm` for tests:
```go
mock := varnishadm.NewMock(0, "", slog.Default())
mock.SetResponse("custom.cmd", varnishadm.VarnishResponse{...})
resp, _ := mock.Exec("ping")
```
