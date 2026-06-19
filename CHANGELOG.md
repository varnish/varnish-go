# Changelog

## v0.1.1 — 2026-06-19

- **Breaking**: all `adm.Conn` methods, `Connect`, `ConnectRaw`, and `Accept` now take `context.Context` as their first argument; context deadline is forwarded to the connection, and cancellation interrupts in-progress I/O by expiring the connection deadline
- **New**: `adm.Conn.Version()` — queries the admin banner and returns `BannerVersion` with `IsEnterprise bool`, `Version string`, and `Revision string`; errors if the version line cannot be parsed
- **New**: `adm.TLSCertEntry` — extended with Varnish Enterprise fields (`Name`, `Expiry`, `Staple`, `ClientVerify`, `CRL`); `TLSCertList` now parses both Varnish Cache (flat array) and Varnish Enterprise (nested `frontends`/`fqdns`) output, branching on `Conn.Version()`

## v0.0.14 — 2026-06-16

- **Breaking**: `adm.TLSCertLoad` — second argument changed from `keyFile string` to `opts ...TLSOption`; accepts optional cert ID, frontend, key file, protocols, ciphers, cipher suites, default-cert flag, and server-cipher-order flag

## v0.0.13 — 2026-06-16

- **New**: add `VCLTemperature.String()` to `adm.Conn`

## v0.0.12 — 2026-06-02

- **New**: add `TLSCertList` and `TLSCertDiscard` to  `adm.Conn`

## v0.0.11 — 2026-05-30

- **New**: `adm.Conn` now has typed methods for (almost) all known `varnishadm` commands

## v0.0.10 — 2026-05-26

- **New**: `vtest` now has TLS helper to start `varnish` listening to HTTPS
 
## v0.0.9 — 2026-05-20

- **New**: `log` — named tag variables (`log.TagReqURL`, `log.TagRespStatus`, …) covering the union of all known VSL tags across Varnish OSS and Enterprise; values resolved at init via `VSL_Name2Tag`, zero if absent from the installed version

## v0.0.8 — 2026-05-13

- **New**: `vtest` — `CounterChecker.AssertEquals(t, n)` calls `t.Fatal` if the counter doesn't reach `n`
- **Fix**: `vtest` — example and test cleanup for `AssertStart` and `CounterChecker`

## v0.0.7 — 2026-05-12

- **New**: `vtest` — `VarnishBuilder.AssertStart(t)` calls `t.Fatal` if Varnish fails to start; stderr/stdout logs included in the error message
- **New**: `vtest` — `CounterChecker` — wait for VSC counters to satisfy conditions (`Equals`, `AtLeast`, `AtMost`, `GreaterThan`, `LessThan`, `WithTestFunction`)
- `vtest` now captures stdout/stderr from the Varnish process; access via `SysLogs()`
- `vtest` handles Varnish license files (Varnish Enterprise)

## v0.0.6 — 2026-05-11

- **Fix**: `log` — include compilation error output in returned error message
- **Fix**: `log` — remove unused `nextRecord` helper

## v0.0.5 — 2026-05-11

- **New**: `version` package — reports installed Varnish edition (open-source or Enterprise), version string, and commit hash at compile time via `vmod_abi.h`
- **Fix**: `log` — `VSL_Next` no longer called on uninitialized cursors; Varnish Enterprise provides non-NULL cursors with a NULL `priv_tbl` that would otherwise trigger an internal assert
- **CI**: test matrix now covers both Varnish and Varnish Enterprise; fail-fast disabled

## v0.0.4 — 2026-05-08

- **New**: `log` package — stream and filter VSL transactions from a live Varnish instance or binary file, equivalent to `varnishlog`
- `vtest` now collects VSL records in the background; access them via `Records()`, `RecordChannel()`, or `TransactionChannel()`

## v0.0.3 — 2026-05-07

- **Changed**: `stat.Update()` is now only required to detect newly added or removed counters; existing counters update in place without an explicit call

## v0.0.2 — 2026-05-06

- **New**: `stat` package — poll VSC counters from Varnish Shared Memory, equivalent to `varnishstat`

## v0.0.1 — 2025-09-26

- Initial release
- `vtest` package — spawn ephemeral Varnish instances for testing VCL, Go-native alternative to the `varnishtest` tool
- `adm` package — admin socket client, equivalent to `varnishadm`
