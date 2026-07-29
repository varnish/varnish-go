package vcl_test

import (
	"strings"
	"testing"

	"github.com/varnish/varnish-go/pkg/vcl"
)

const simpleVCL = `vcl 4.0;

backend default {
    .host = "127.0.0.1";
    .port = "8080";
}

sub vcl_recv {
    if (req.method == "GET") {
        return (hash);
    }
}
`

func TestVclParser_RoundTrip(t *testing.T) {
	v, err := vcl.NewParser(simpleVCL).Filename("simple.vcl").Parse()
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}

	ast := v.AST()
	if ast == nil {
		t.Fatal("AST() returned nil")
	}
	if ast.VCLVersion == nil {
		t.Fatal("AST() has no VCLVersion declaration")
	}
	if len(ast.Declarations) == 0 {
		t.Fatal("AST() has no declarations")
	}

	out := v.String()
	if !strings.Contains(out, "vcl 4.0") {
		t.Errorf("String() = %q, want it to contain %q", out, "vcl 4.0")
	}
	if !strings.Contains(out, "backend default") {
		t.Errorf("String() = %q, want it to contain %q", out, "backend default")
	}
}

func TestVclParser_AllowMissingVersion(t *testing.T) {
	const noVersion = `
sub vcl_recv {
    return (hash);
}
`
	if _, err := vcl.NewParser(noVersion).Parse(); err == nil {
		t.Fatal("Parse() with missing version declaration: got nil error, want an error")
	}

	if _, err := vcl.NewParser(noVersion).AllowMissingVersion(true).Parse(); err != nil {
		t.Fatalf("Parse() with AllowMissingVersion(true): unexpected error: %v", err)
	}
}

func TestVclParser_DisableInlineC(t *testing.T) {
	const withInlineC = `vcl 4.0;

sub vcl_recv {
    C{ printf("hi"); }C
}
`
	if _, err := vcl.NewParser(withInlineC).Parse(); err != nil {
		t.Fatalf("Parse() with inline C by default: unexpected error: %v", err)
	}

	if _, err := vcl.NewParser(withInlineC).DisableInlineC(true).Parse(); err == nil {
		t.Fatal("Parse() with DisableInlineC(true): got nil error, want an error")
	}
}

func TestVclParser_ChainedOptions(t *testing.T) {
	// Smoke test: every option method is threaded through to the underlying
	// parser.Option and doesn't break parsing of valid input.
	_, err := vcl.NewParser(simpleVCL).
		Filename("chained.vcl").
		MaxErrors(1).
		SkipSubroutineValidation(true).
		IncludeMaxDepth(1).
		Parse()
	if err != nil {
		t.Fatalf("Parse() with chained options: unexpected error: %v", err)
	}
}
