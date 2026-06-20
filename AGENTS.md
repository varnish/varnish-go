# Agent Guide — varnish-go

Go SDK for Varnish. Primary goal: let Go programs test VCL logic by spawning
real Varnish instances, as an alternative to the `varnishtest` DSL tool.

Module: `github.com/varnish/varnish-go` (Go 1.25.1)

---

## Prerequisites

All packages use CGo and link against **libvarnishapi** via `pkg-config`.
Nothing will compile without the Varnish development headers installed.

**Open-source Varnish:**
```bash
curl -Ls https://packages.varnish-software.com/varnish/bootstrap-deb.sh | sudo sh
sudo apt-get install varnish varnish-dev
```

**Varnish Plus (Enterprise):**
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

When running against Varnish Plus, pass `-tags varnish_plus` to skip tests
that use the OSS-only binary fixture `log/testdata/test1_log.bin`:

```bash
go test -tags varnish_plus ./...
```

No Makefile, no code generation, no linter config beyond standard `gofmt`. CI
runs `go doc ./...` to verify package docs compile cleanly.

`log/tags.go` declares a `var TagFoo Tag` for every known VSL tag across Varnish
OSS and Enterprise. `init()` resolves each name via `VSL_Name2Tag`; tags absent
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
| `version` | Reports the installed Varnish edition (OSS vs. Plus), version string, and commit hash from `vmod_abi.h` at compile time |
| `vtest`   | Spawns ephemeral Varnish instances for testing — the primary reason this repo exists |

---

## vtest — Spawning Varnish Instances

`vtest` is the core package. Use a fluent builder to configure and start an
instance, then make real HTTP requests against it:

```go
varnish, err := vtest.New().
    Backend("origin", svr.URL).   // inject a backend URL into the VCL
    VCL(`sub vcl_recv { ... }`).  // optional custom VCL
    Start()
if err != nil {
    t.Fatal(err)
}
defer varnish.Stop()

resp, err := http.Get(varnish.URL)
```

`Start()` spawns `varnishd` in a temporary workdir under `/tmp/`, waits for it
to be ready, and begins collecting VSL records in the background.

Access VSL data after the fact:
- `varnish.Records()` — all records collected so far (slice)
- `varnish.RecordChannel()` — live channel of incoming records
- `varnish.TransactionChannel()` — live channel of grouped transactions

Call `varnish.Stop()` to terminate the process and clean up the workdir.

---

## version Package

Use `version` to branch behavior at runtime based on the installed Varnish:

```go
import "github.com/varnish/varnish-go/version"

version.IsEnterprise() // true if Varnish Plus
version.Version()      // e.g. "9.0.0" or "6.0.17r3"
version.Commit()       // git commit hash
```

The values are resolved at compile time from `VMOD_ABI_Version` in `vmod_abi.h`
and cached in package-level variables at `init()`.

---

## Testing Against Varnish Plus (Docker)

To test against Varnish Enterprise without installing it locally, start a
container once and exec into it:

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
