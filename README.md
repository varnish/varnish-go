# Go SDK for Varnish

**Important**: this is a work-in-progress. Bugs and feature requests welcome via [issues](https://github.com/varnish/varnish-go/issues) or [pull requests](https://github.com/varnish/varnish-go/pulls).

All packages require [libvarnishapi](https://packagecloud.io/varnishplus) (open-source or Enterprise) and CGo.

## Packages

### [`vtest`](https://pkg.go.dev/github.com/varnish/varnish-go/vtest) — spawn Varnish instances for testing

Go-native alternative to the [`varnishtest`](https://varnish-cache.org/docs/trunk/reference/varnishtest.html) tool.
Spin up a real Varnish process, make HTTP requests against it, and inspect VSL logs — all from regular Go tests.

```shell
go get github.com/varnish/varnish-go/vtest
```

### [`log`](https://pkg.go.dev/github.com/varnish/varnish-go/log) — stream VSL transactions

Read and filter [Varnish Shared Log](https://varnish-cache.org/docs/trunk/reference/vsl.html) transactions from a live instance or a binary file, equivalent to `varnishlog`.

```shell
go get github.com/varnish/varnish-go/log
```

### [`stat`](https://pkg.go.dev/github.com/varnish/varnish-go/stat) — read statistics counters

Poll VSC counters from Varnish Shared Memory, equivalent to `varnishstat`.

```shell
go get github.com/varnish/varnish-go/stat
```

### [`adm`](https://pkg.go.dev/github.com/varnish/varnish-go/adm) — admin socket client

Send CLI commands to a running Varnish instance, equivalent to `varnishadm`.

```shell
go get github.com/varnish/varnish-go/adm
```

### [`version`](https://pkg.go.dev/github.com/varnish/varnish-go/version) — installed Varnish version

Reports the installed Varnish edition (open-source or Enterprise), version string, and commit hash, resolved at compile time from `vmod_abi.h`.

```shell
go get github.com/varnish/varnish-go/version
```
