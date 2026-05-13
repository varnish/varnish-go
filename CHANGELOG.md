# Changelog

## v0.0.8 (unreleased)

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
