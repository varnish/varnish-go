# Go SDK for Varnish

**Important**: this is a work-in-progress. There will be bugs, if you find one in the wild or if you have a feature request, please do open [an issue](https://github.com/varnish/varnish-go/issues) for it, or even better, open a [pull request](https://github.com/varnish/varnish-go/pulls).

The primary goal of this project is to provide an alternative to the [varnishtest](https://varnish-cache.org/docs/trunk/reference/varnishtest.html) tool.
The original, varnish-bundled too is invaluable for testing your VCL logic and [Varnish](https://varnish-cache.org/) in general (the [project itself](https://github.com/varnishcache/varnish-cache/tree/master/bin/varnishtest/tests) uses it for hundreds of tests in CI) but it will require you to learn the [Domain Specific Language](https://varnish-cache.org/docs/trunk/reference/vtc.html).

Offering a `go` SDK for it will hopefully accelerate your velocity and broaden your code-coverage of you VCL.

# Installation

As with all `go` packages:

``` shell
go get github.com/varnish/varnish-go/adm
```

# Example

``` go
package main

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"

	"github.com/varnish/varnish-go/vtest"
)

func main() {
	// create a test backend
	svr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "this is my body")
	}))                                                                                                                                                                           

	// add the backend definition to the loaded VCL                                                                                                                                                       
	varnish, err := vtest.New().Backend("svr", svr.URL).Start()
	if err != nil {
		panic(err)
	}
	defer varnish.Stop()

	resp, err := http.Get(varnish.URL)                                                                                                                             
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		panic(err)
	}

	fmt.Printf("response body: %s\n", string(body))
}
```
