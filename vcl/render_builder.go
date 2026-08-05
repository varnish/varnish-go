package vcl

import (
	"github.com/varnish/varnish-go/sdk/vcl/ast"
	"github.com/varnish/varnish-go/sdk/vcl/renderer"
)

// VclRenderBuilder configures how an already-parsed Vcl is rendered back to VCL source.
// Create one with Vcl.RenderBuilder, chain the option methods, then call Render.
type VclRenderBuilder struct {
	program        *ast.Program
	indentWidth    int
	removeComments bool
}

// RenderBuilder creates a VclRenderBuilder for this Vcl's parsed AST.
func (v *Vcl) RenderBuilder() *VclRenderBuilder {
	return &VclRenderBuilder{program: v.program, indentWidth: 4}
}

// SpaceIndent sets the number of spaces used per indentation level. 0 renders flat, with no
// indentation. Default: 4.
func (b *VclRenderBuilder) SpaceIndent(n uint) *VclRenderBuilder {
	b.indentWidth = int(n)
	return b
}

// RemoveComments controls whether comments are stripped from the rendered output. Default:
// false (comments preserved).
func (b *VclRenderBuilder) RemoveComments(remove bool) *VclRenderBuilder {
	b.removeComments = remove
	return b
}

// Render renders the configured Vcl back to VCL source.
func (b *VclRenderBuilder) Render() string {
	return renderer.Render(b.program,
		renderer.WithIndentWidth(b.indentWidth),
		renderer.WithoutComments(b.removeComments),
	)
}
