package vcl_test

import (
	"strings"
	"testing"

	"github.com/varnish/varnish-go/vcl"
)

func TestVclRenderBuilder_SpaceIndent(t *testing.T) {
	v, err := vcl.NewParser(simpleVCL).Parse()
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}

	two := v.RenderBuilder().SpaceIndent(2).Render()
	if !strings.Contains(two, "\n  .host") {
		t.Errorf("SpaceIndent(2): expected 2-space indented .host property, got:\n%s", two)
	}

	four := v.RenderBuilder().SpaceIndent(4).Render()
	if !strings.Contains(four, "\n    .host") {
		t.Errorf("SpaceIndent(4): expected 4-space indented .host property, got:\n%s", four)
	}

	flat := v.RenderBuilder().SpaceIndent(0).Render()
	if !strings.Contains(flat, "\n.host") {
		t.Errorf("SpaceIndent(0): expected no indentation on .host property, got:\n%s", flat)
	}
}

func TestVclRenderBuilder_RemoveComments(t *testing.T) {
	const withComment = `vcl 4.0;

// a leading comment
backend default {
    .host = "127.0.0.1";
}
`
	v, err := vcl.NewParser(withComment).Parse()
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}

	kept := v.RenderBuilder().Render()
	if !strings.Contains(kept, "a leading comment") {
		t.Errorf("RemoveComments default: expected comment to be preserved, got:\n%s", kept)
	}

	preserved := v.RenderBuilder().RemoveComments(false).Render()
	if !strings.Contains(preserved, "a leading comment") {
		t.Errorf("RemoveComments(false): expected comment to be preserved, got:\n%s", preserved)
	}

	stripped := v.RenderBuilder().RemoveComments(true).Render()
	if strings.Contains(stripped, "a leading comment") {
		t.Errorf("RemoveComments(true): expected comment to be stripped, got:\n%s", stripped)
	}
}

func TestVcl_String_MatchesDefaultRenderBuilder(t *testing.T) {
	v, err := vcl.NewParser(simpleVCL).Parse()
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}

	got := v.String()
	want := v.RenderBuilder().SpaceIndent(4).Render()
	if got != want {
		t.Errorf("String() = %q, want %q (RenderBuilder().SpaceIndent(4).Render())", got, want)
	}
}
