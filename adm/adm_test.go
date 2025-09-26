package adm_test

import (
	"fmt"
	"github.com/varnish/varnish-go/adm"
)

// Check the running state of the child
func Example() {
	// use the default instance (/usr/bin/varnish/varnishd)
	conn, err := adm.Connect("")
	if err != nil {
		panic(err)
	}
	response, err := conn.Ask("status")
	if err != nil {
		panic(err)
	}
	fmt.Printf("response: %s", response)
}

// Connect to an instance with a non-default workdir
func ExampleConnect() {
	// using a specific name (use the same argument as "-n" for varnishd)
	conn, err := adm.Connect("/tmp/varnish_test_instances")
	if err != nil {
		panic(err)
	}
	fmt.Printf("cli endpoint: %s\n", conn.RemoteAddr().String())
}
