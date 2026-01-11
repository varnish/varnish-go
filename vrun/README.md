# vrun

Manages varnishd process lifecycle.

## Types

- `Manager` - Handles workspace preparation, secret generation, and process execution
- `Config` - Configuration for building varnishd command-line arguments

## Usage

```go
logger := slog.Default()
mgr := vrun.New("/var/run/varnish", logger, "/var/lib/varnish/instance")

if err := mgr.PrepareWorkspace(""); err != nil {
    log.Fatal(err)
}

cfg := &vrun.Config{
    WorkDir:    "/var/run/varnish",
    AdminPort:  6082,
    VarnishDir: "/var/lib/varnish/instance",
    Listen:     []string{":8080,http", ":443,https"},
    Storage:    []string{"malloc,256m"},
    Params:     map[string]string{"thread_pool_min": "10"},
}
args := vrun.BuildArgs(cfg)

ctx := context.Background()
if err := mgr.Start(ctx, "", args); err != nil {
    log.Fatal(err)
}
```

## Notes

- `Start()` blocks until varnishd exits; use context cancellation to stop
- VCL is not loaded at startup (`-f ""`); load via admin socket after start
- Secret file is written to `WorkDir/secret`
- License file (Varnish Enterprise) is written to `WorkDir/orca.lic` if provided
