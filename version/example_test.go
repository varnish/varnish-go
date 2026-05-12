package version_test

import (
	"fmt"

	"github.com/varnish/varnish-go/version"
)

func Example() {
	fmt.Printf("Varnish %s (%s)\n", version.Version(), version.Commit())
	if version.IsEnterprise() {
		fmt.Println("Enterprise edition")
	}
}
