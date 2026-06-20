package adm_test

import (
	"context"
	"fmt"
	"github.com/varnish/varnish-go/adm"
)

// Check the running state of the child
func Example() {
	// use the default instance (/usr/bin/varnish/varnishd)
	conn, err := adm.Connect(context.Background(), "")
	if err != nil {
		panic(err)
	}
	response, err := conn.Ask(context.Background(), "status")
	if err != nil {
		panic(err)
	}
	fmt.Printf("response: %s", response)
}

// Connect to an instance with a non-default workdir
func ExampleConnect() {
	// using a specific name (use the same argument as "-n" for varnishd)
	conn, err := adm.Connect(context.Background(), "/tmp/varnish_test_instances")
	if err != nil {
		panic(err)
	}
	fmt.Printf("cli endpoint: %s\n", conn.RemoteAddr().String())
}
