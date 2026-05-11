// Reports the installed Varnish edition
package version

// #cgo pkg-config: varnishapi
// #include <vmod_abi.h>
//
// static const char *abi_version_string(void) { return VMOD_ABI_Version; }
import "C"

import "strings"

var (
	isEnterprise bool
	version      string
	commit       string
)

func init() {
	isEnterprise, version, commit = parse(C.GoString(C.abi_version_string()))
}

// parse splits a VMOD_ABI_Version string into its components.
//
// "Varnish Plus 6.0.17r3 <commit>" → true,  "6.0.17r3", "<commit>"
// "Varnish 9.0.0 <commit>"         → false, "9.0.0",    "<commit>"
func parse(s string) (enterprise bool, ver, rev string) {
	f := strings.Fields(s)
	switch {
	case len(f) >= 4 && f[1] == "Plus":
		return true, f[2], f[3]
	case len(f) >= 3:
		return false, f[1], f[2]
	default:
		return false, "", ""
	}
}

// IsEnterprise reports whether the installed Varnish is Varnish Plus.
func IsEnterprise() bool { return isEnterprise }

// Version returns the Varnish version string (e.g. "9.0.0" or "6.0.17r3").
func Version() string { return version }

// Commit returns the Varnish git commit hash.
func Commit() string { return commit }
