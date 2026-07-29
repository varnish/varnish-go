package vcl_test

import (
	"fmt"

	"github.com/varnish/varnish-go/pkg/vcl"
)

func Example() {
	const src = `vcl 4.0;

backend default {
    .host = "127.0.0.1";
    .port = "8080";
}
`

	v, err := vcl.NewParser(src).Parse()
	if err != nil {
		fmt.Println("parse error:", err)
		return
	}

	fmt.Println(v.AST().VCLVersion.Version)
	fmt.Println(len(v.AST().Declarations))

	// Output:
	// 4.0
	// 1
}
