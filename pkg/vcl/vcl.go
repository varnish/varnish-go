package vcl

import (
	"github.com/varnish/varnish-go/pkg/vcl/ast"
	"github.com/varnish/varnish-go/pkg/vcl/parser"
	"github.com/varnish/varnish-go/pkg/vcl/renderer"
)

// VclParser builds a Vcl by configuring and running the underlying parser.
// Use NewParser to create one, chain the option methods, then call Parse.
type VclParser struct {
	input    string
	filename string
	opts     []parser.Option
}

// NewParser creates a VclParser for the given VCL source.
func NewParser(input string) *VclParser {
	return &VclParser{input: input, filename: "input.vcl"}
}

// Filename sets the filename reported in parse errors. Defaults to "input.vcl".
func (p *VclParser) Filename(name string) *VclParser {
	p.filename = name
	return p
}

// MaxErrors limits the number of errors before stopping parsing (0 = no limit). Default: 8.
func (p *VclParser) MaxErrors(max int) *VclParser {
	p.opts = append(p.opts, parser.WithMaxErrors(max))
	return p
}

// DisableInlineC disables parsing of C code blocks (C{ }C).
func (p *VclParser) DisableInlineC(disable bool) *VclParser {
	p.opts = append(p.opts, parser.WithDisableInlineC(disable))
	return p
}

// AllowMissingVersion allows parsing VCL without a version declaration, useful for included files.
func (p *VclParser) AllowMissingVersion(allow bool) *VclParser {
	p.opts = append(p.opts, parser.WithAllowMissingVersion(allow))
	return p
}

// SkipSubroutineValidation skips validation of subroutine calls during parsing, useful for
// included files where subroutines may be defined elsewhere.
func (p *VclParser) SkipSubroutineValidation(skip bool) *VclParser {
	p.opts = append(p.opts, parser.WithSkipSubroutineValidation(skip))
	return p
}

// ResolveIncludes enables automatic resolution of include statements after parsing, relative
// to basePath.
func (p *VclParser) ResolveIncludes(basePath string) *VclParser {
	p.opts = append(p.opts, parser.WithResolveIncludes(basePath))
	return p
}

// IncludeMaxDepth sets the maximum depth for resolving nested includes. Default: 10.
func (p *VclParser) IncludeMaxDepth(depth int) *VclParser {
	p.opts = append(p.opts, parser.WithIncludeMaxDepth(depth))
	return p
}

// Parse runs the parser over the configured input and returns the resulting Vcl.
func (p *VclParser) Parse() (*Vcl, error) {
	program, err := parser.Parse(p.input, p.filename, p.opts...)
	if err != nil {
		return nil, err
	}
	return &Vcl{program: program}, nil
}

// Vcl is a parsed VCL program.
type Vcl struct {
	program *ast.Program
}

// String renders the parsed program back to VCL source.
func (v *Vcl) String() string {
	return renderer.Render(v.program)
}

// AST returns the parsed abstract syntax tree.
func (v *Vcl) AST() *ast.Program {
	return v.program
}
