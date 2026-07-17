# Agent Guide — varnish-go

Go SDK for Varnish. Primary goal: let Go programs test VCL logic by spawning
real Varnish instances, as an alternative to the `varnishtest` DSL tool.

Module: `github.com/varnish/varnish-go` (Go 1.25.1)

---

## Prerequisites

All packages use CGo and link against **libvarnishapi** via `pkg-config`.
Nothing will compile without the Varnish development headers installed.

**Varnish Cache:**
```bash
curl -Ls https://packages.varnish-software.com/varnish/bootstrap-deb.sh | sudo sh
sudo apt-get install varnish varnish-dev
```

**Varnish Enterprise:**
```bash
curl -s https://packagecloud.io/install/repositories/varnishplus/60-enterprise/script.deb.sh | sudo INSTALL= bash
sudo apt-get install varnish-plus varnish-plus-dev
```

Both install a `varnishapi.pc` pkg-config file that CGo picks up automatically.
Tests also need `varnishd` in `$PATH` (included in both packages above).

---

## Build & Test

```bash
go build ./...          # compile everything
go test ./...           # run full test suite
```

When running against Varnish Enterprise, pass `-tags varnish_plus` to skip tests
that use the Varnish Cache-only binary fixture `log/testdata/test1_log.bin`:

```bash
go test -tags varnish_plus ./...
```

No Makefile, no code generation, no linter config beyond standard `gofmt`. CI
runs `go doc ./...` to verify package docs compile cleanly.

`log/tags.go` declares a `var TagFoo Tag` for every known VSL tag across Varnish
Cache and Enterprise. `init()` resolves each name via `VSL_Name2Tag`; tags absent
from the installed Varnish version stay zero.

Tests are integration tests — they open the real Varnish shared memory or spawn
live `varnishd` processes. There are no mocked unit tests.

---

## Packages

| Package   | Purpose |
|-----------|---------|
| `adm`     | Admin socket client — connects and authenticates to Varnish's control port (equivalent to `varnishadm`) |
| `log`     | VSL reader — streams and filters Varnish Shared Log transactions using libvarnishapi |
| `stat`    | Statistics reader — reads VSC counters from Varnish Shared Memory (equivalent to `varnishstat`) |
| `version` | Reports the installed Varnish edition (Cache vs. Enterprise), version string, and commit hash from `vmod_abi.h` at compile time |
| `vtest`   | Spawns ephemeral Varnish instances for testing — the primary reason this repo exists |

---

## vtest — Spawning Varnish Instances

`vtest` is the core package. Use a fluent builder to configure and start an
instance, then make real HTTP requests against it:

```go
varnish := vtest.New().
    Backend("origin", svr.URL).        // inject a backend URL into the VCL
    VclString(`sub vcl_recv { ... }`). // optional custom VCL (version and backends prepended automatically)
    AssertStart(t)                      // calls t.Fatal on failure; includes SysLogs in error
defer varnish.Stop()

resp, err := http.Get(varnish.URL)
```

`Start()` / `AssertStart(t)` spawns `varnishd` in a temporary workdir (created
with `os.MkdirTemp`, honoring `TMPDIR`), waits for it to be ready, and begins
collecting VSL records in the background. `Stop()` must always be called (use
`defer`). A failed `Start` cleans up after itself: the varnishd process is
terminated and the workdir removed.

### VarnishBuilder methods (all return `*VarnishBuilder` for chaining)

| Method | Purpose |
|--------|---------|
| `New()` | Create builder; defaults to VCL 4.1, no backends |
| `VclString(s)` | Provide VCL as string; version header and backend defs are prepended |
| `VclFile(path)` | Load VCL from file (loaded as-is, no prepending) |
| `VCLVersion(s)` | Override VCL version header (default: `"vcl 4.1;\n\n"`) |
| `Vcl41()` / `Vcl40()` | Shorthand version setters |
| `Backend(name, url)` | Add a VCL backend; panics if URL cannot be parsed |
| `Parameter(name, val)` | Append a `-p name=val` flag to varnishd |
| `TLSListener()` | Add a TLS listener; endpoint available via `v.TLSURL` after start |
| `PEMFile(cert, key)` | Register a TLS cert for auto-loading (implicitly enables TLSListener; key may be `""` if embedded in cert) |
| `SetLicensePath(path)` | Set `VARNISH_LICENSE` env var for Varnish Enterprise |
| `NoRecordLogs()` | Disable background VSL record collection (saves resources for long-running tests) |
| `NoSysLogs()` | Disable stdout/stderr accumulation |
| `SysLogChannel()` | Get a `<-chan string` of stdout/stderr lines from startup |
| `Start()` | Start instance; returns `(Varnish, error)` |
| `AssertStart(t)` | Start instance; calls `t.Fatal` with SysLogs on failure |

### Varnish methods

| Method | Purpose |
|--------|---------|
| `v.URL` / `v.TLSURL` | HTTP / HTTPS endpoint (set after start) |
| `v.Name()` | Workdir path (passed as `-n` to varnishd) |
| `v.Stop()` | Terminate process and clean up workdir |
| `v.AdmConn()` | Return `*adm.Conn` for direct admin commands |
| `v.Adm(args...)` | `adm.Conn.Ask` passthrough |
| `v.AdmRaw(args...)` | `adm.Conn.AskRaw` passthrough |
| `v.WaitRunning()` | Block until child is running (called automatically by Start) |
| `v.Records()` | Snapshot of all VSL records collected so far |
| `v.RecordChannel()` | Live `<-chan vsl.Record` (GroupingRaw) |
| `v.TransactionChannel()` | Live `<-chan vsl.Transaction` (GroupingVXID) |
| `v.SysLogs()` | Snapshot of stdout/stderr lines since start |
| `v.SysLogChannel()` | Live `<-chan string` of stdout/stderr lines from point of subscription |
| `v.Counter(name)` | Return a `*CounterChecker` for a VSC counter (e.g. `"MAIN.cache_hit"`) |

### CounterChecker (fluent, terminal methods block until condition or timeout)

```go
v.Counter("MAIN.cache_hit").TryFor(10*time.Second).TryEvery(200*time.Millisecond).AtLeast(5)
v.Counter("MAIN.cache_hit").AssertEquals(t, 3)
```

| Method | Purpose |
|--------|---------|
| `TryFor(d)` | Max wait duration (default 5s) |
| `TryEvery(d)` | Poll interval (default 100ms) |
| `MustExist()` | Fail immediately if counter not found |
| `Value()` | Wait for counter to appear and return value |
| `Equals(n)` / `AssertEquals(t, n)` | Wait until value == n |
| `NotEquals(n)` | Wait until value != n |
| `AtLeast(n)` / `AtMost(n)` | Wait until value >= / <= n |
| `GreaterThan(n)` / `LessThan(n)` | Wait until value > / < n |
| `WithTestFunction(f)` | Wait until `f(value)` returns true |

---

## adm — Admin Socket Client

`adm.Conn` wraps a TCP connection to varnishd's management socket. All methods
take `context.Context` as the first argument; the deadline is forwarded to the
socket and cancellation interrupts in-progress I/O immediately.

### Constructors (all return `*Conn`)

```go
// Auto-discover endpoint from workdir (equivalent to varnishadm -n name)
conn, err := adm.Connect(ctx, name)       // name="" uses "varnishd"; relative names resolved under /var/lib/varnish

// Explicit endpoint and secret file
conn, err := adm.ConnectRaw(ctx, addrPort, secretPath)

// Accept a connection from varnishd (-m flag)
conn, err := adm.Accept(ctx, listener, secretPath)
```

Authentication uses SHA256 challenge-response (same as `varnishadm`).

### Low-level methods

| Method | Returns | Purpose |
|--------|---------|---------|
| `Ask(ctx, args...)` | `(string, error)` | Send command; join args with spaces; error if status != 200 |
| `AskRaw(ctx, args...)` | `(int, []byte, error)` | Same but return raw status code and message bytes |
| `ReadMessage(ctx)` | `(int, []byte, error)` | Read next message (used internally; needed for raw socket setups) |

### VCL methods

| Method | Purpose |
|--------|---------|
| `VCLList(ctx)` | All loaded VCLs and labels as `map[string]VCLEntry` |
| `VCLDeps(ctx)` | Dependency map: VCL name → list of names it depends on |
| `VCLLoad(ctx, name, file, state)` | Compile and load VCL file |
| `VCLInline(ctx, name, vcl, state)` | Compile and load VCL source string (heredoc; marker is SHA256 of content) |
| `VCLUse(ctx, name)` | Switch active VCL |
| `VCLDiscard(ctx, names...)` | Unload VCLs; stops on first failure |
| `VCLLabel(ctx, label, config)` | Apply symbolic label to a VCL config |
| `VCLSetState(ctx, name, state)` | Force VCL temperature |
| `VCLShow(ctx, name)` | Return source files of named VCL as `[]VCLFile`; empty name = active VCL |
| `VCLSymtab(ctx)` | Dump VCL symbol tables (debug) |

`VCLState`: `VCLStateAuto` (default), `VCLStateCold`, `VCLStateWarm`.

`VCLTemperature` (read from varnishd): `VCLTempUnknown`, `VCLTempInit`,
`VCLTempCold`, `VCLTempWarm`, `VCLTempBusy`, `VCLTempCooling`.

### Backend methods

| Method | Purpose |
|--------|---------|
| `BackendList(ctx)` | All backends as `map[string]BackendEntry` (always requests probe details) |
| `BackendSetHealth(ctx, pattern, state)` | Set health for backends matching glob pattern (`[A-Za-z0-9._*]+`; `*` selects all) |

`ProbeHealth`: `ProbeUnknown`, `ProbeHealthy`, `ProbeSick`, `ProbeProbe` (maps to `"auto"` in `BackendSetHealth`).

`BackendEntry`: `FullName`, `VCL`, `Name`, `Admin ProbeHealth`, `Probe *ProbeResult`, `LastChange time.Time`.

### Ban methods

| Method | Purpose |
|--------|---------|
| `BanList(ctx)` | All active bans as `[]BanEntry` |
| `Ban(ctx, expression)` | Create ban; expression format: `"field op arg [&& ...]"` |

`BanEntry`: `Time time.Time`, `Refs int`, `Completed bool`, `Spec string`.

### Parameter methods

| Method | Purpose |
|--------|---------|
| `ParamShow(ctx)` | All runtime parameters as `map[string]ParamInfo` |
| `ParamShowChanged(ctx)` | Only parameters differing from compiled-in defaults |
| `ParamSet(ctx, param, value)` | Set parameter; returns updated `ParamInfo` |
| `ParamReset(ctx, param)` | Reset to default; falls back to plain text on editions without JSON support |

`ParamInfo`: `Name`, `Implemented bool`, `Value any`, `Default`, `Units`, `Minimum`, `Maximum`, `Description`, `Flags []string`.

### TLS methods (Varnish Enterprise)

| Method | Purpose |
|--------|---------|
| `TLSCertList(ctx)` | All loaded cert bindings as `[]TLSCertEntry`; handles Cache vs Enterprise response formats automatically |
| `TLSCertLoad(ctx, certFile, opts...)` | Stage a certificate for commit |
| `TLSCertCommit(ctx)` | Apply all staged certificate changes |
| `TLSCertRollback(ctx)` | Discard all staged certificate changes |
| `TLSCertDiscard(ctx, id)` | Mark a certificate for removal |

`TLSOption` builder functions: `TLSWithCertID(id)`, `TLSWithFrontend(name)`,
`TLSWithKeyFile(path)`, `TLSWithProtocols(protos...)`, `TLSWithCiphers(ciphers...)`,
`TLSWithCipherSuites(suites...)`, `TLSWithDefaultCert()`, `TLSWithServerCipherOrder()`.

### Misc methods

| Method | Purpose |
|--------|---------|
| `Status(ctx)` | Running state of child process (`"running"`, `"stopped"`, …) |
| `Ping(ctx)` | Verify connection is alive |
| `PID(ctx)` | Master and worker PIDs as `PIDResponse{Master, Worker int}` |
| `Start(ctx)` / `Stop(ctx)` | Start / stop the varnishd worker process |
| `Banner(ctx)` | Raw welcome banner string |
| `Version(ctx)` | `BannerVersion{IsEnterprise bool, Version, Revision string}`; cached after first call |
| `Quit(ctx)` | Close admin connection |
| `PanicShow(ctx)` | Last panic message or `""` |
| `PanicClear(ctx, resetCounters)` | Clear last panic; status 300 (no panic) treated as success |

---

## log — VSL Reader

Streams Varnish Shared Log transactions using libvarnishapi.

```go
reader, err := log.New().
    SetName("varnishd").
    SetQuery(`ReqURL ~ "^/api"`).
    SetGrouping(log.GroupingVXID).
    Attach()
if err != nil {
    return err
}
defer reader.Close()

err = reader.Run(ctx, func(txns []log.Transaction) error {
    for _, txn := range txns {
        for _, rec := range txn.Records {
            fmt.Println(rec.Tag, rec.Data)
        }
    }
    return nil
})
```

### LogReaderBuilder methods

| Method | Purpose |
|--------|---------|
| `SetName(name)` | Varnish instance workdir name |
| `SetTimeout(d)` | Attach wait duration; negative disables |
| `SetQuery(q)` | VSL query filter expression (see `vsl-query(7)`) |
| `SetGrouping(g)` | `GroupingRaw`, `GroupingVXID` (default), `GroupingRequest`, `GroupingSession` |
| `SetErrHandler(f)` | Callback for recoverable errors (`ErrOverrun`, `ErrAbandoned`, `ErrWorkerRestarted`, `ErrCursorLost`, `ErrIO`) |
| `SetBacklog(bool)` | Process existing buffered records before tailing (equivalent to `varnishlog -d`) |
| `SetLive(bool)` | `true`: follow indefinitely; `false`: stop after catching up to tail |
| `SetFile(path)` | Read from binary VSL file instead of live instance |
| `Attach()` | Connect and return `*LogReader` |

### LogReader

- `Run(ctx, func([]Transaction) error) error` — stream transactions; non-nil handler return stops processing; handles worker restarts transparently
- `Close()` — release resources; call exactly once

### Key types

`Record`: `Tag Tag`, `VXID uint64`, `IsClient bool`, `IsBackend bool`, `Data string`.

`Transaction`: `Level int`, `VXID int64`, `ParentVXID int64`, `Type TransactionType`, `Reason Reason`, `Records []Record`.

`TransactionType`: `TypeSession`, `TypeRequest`, `TypeBackend`, `TypeRaw`, `TypeUnknown`.

`Reason`: `ReasonHTTP1`, `ReasonRxReq`, `ReasonESI`, `ReasonRestart`, `ReasonPass`, `ReasonFetch`, `ReasonBgFetch`, `ReasonPipe`, `ReasonUnknown`.

### Tags

`log/tags.go` exports `var TagFoo Tag` for every known VSL tag across Varnish Cache and Enterprise (100+ variables). Tags absent from the installed version stay zero — check before use:

```go
if log.TagYKEY == 0 {
    // not supported on this Varnish installation
}
```

Look up a tag by name: `log.TagByName("ReqURL")` (case-insensitive, prefix match accepted).

---

## stat — VSC Counter Reader

Reads VSC counters from Varnish Shared Memory.

```go
reader, err := stat.New().SetName("varnishd").Attach()
if err != nil {
    return err
}
defer reader.Close()

added, removed, err := reader.Update()  // refresh Stats map
val, err := reader.Counter("MAIN.cache_hit")
```

`Counter.Value` is a `*uint64` pointing directly into VSM — it updates continuously without calling `Update()`. `Update()` is only needed to detect newly added or removed counters.

`Counter` fields: `SDesc string`, `LDesc string`, `Value *uint64`, `Semantics`, `Flags`.

`Semantics`: `SemanticsCounter` (monotonic), `SemanticsGauge`, `SemanticsBitmap`, `SemanticsBoolean`.

`Flags`: `FlagsInteger`, `FlagsBytes`, `FlagsBitmap`, `FlagsBoolean`, `FlagsDuration`.

---

## version Package

```go
import "github.com/varnish/varnish-go/version"

version.IsEnterprise() // true if Varnish Enterprise
version.Version()      // e.g. "9.0.0" or "6.0.17r3"
version.Commit()       // git commit hash
```

Values are resolved at `init()` from `VMOD_ABI_Version` in `vmod_abi.h` and cached in package-level variables.

---

## Testing Against Varnish Enterprise (Docker)

To test against Varnish Enterprise without installing it locally:

```bash
# Start container (one time)
docker run -d --name varnish-plus-test \
  --user root \
  -v "$(pwd):/work/" \
  varnish/varnish-enterprise \
  sleep infinity

# Install dev package (one time per container lifetime)
docker exec varnish-plus-test bash -c "
  apt-get update -qq &&
  apt-get install -y varnish-plus-dev
"

# Run tests
docker exec -w /work varnish-plus-test go test -tags varnish_plus ./...

# Interactive shell
docker exec -it -w /work varnish-plus-test bash

# Teardown
docker rm -f varnish-plus-test
```

---

## Conventions

Spell out field and variable names — do not use abbreviations or single-letter
suffixes.

- `sync.Mutex` fields: use the full word (e.g. `versionMutex`, not `versionMu` or `mu`)
- `context.Context` parameters: always named `ctx`
- `error` return values: always named `err`

In code comments and documentation, use full product names:
- **"Varnish Cache"** — not CE, VC, OSS, or "open-source Varnish"
- **"Varnish Enterprise"** — not VE, Plus, or "Varnish Plus"

---

## Changelog

`CHANGELOG.md` uses this format:

```
## v0.0.X — YYYY-MM-DD

- **Label**: `package` — description; additional detail if needed
```

**Labels** (bold): `Breaking`, `New`, `Fix`, `Changed`, `CI`.

Rules:
- One bullet per logical change. Separate label, package, and description with ` — `.
- Unreleased work goes under the current version header, not an "Unreleased" section.
- Bump the version number in the header when cutting a release.

---

## CI

Two matrix variants in `.github/workflows/ci.yml`, each running:
`checkout → install Go → install Varnish → build → test → doc`

| Variant       | Packages installed              | Test flags           |
|---------------|---------------------------------|----------------------|
| `varnish`     | `varnish varnish-dev`           | _(none)_             |
| `varnish-plus`| `varnish-plus varnish-plus-dev` | `-tags varnish_plus` |
