package vtest

import (
	"reflect"
	"testing"
)

func TestSetEnvVar(t *testing.T) {
	environ := []string{"FOO=bar", "VARNISH_LICENSE=/old/path"}

	environ = setEnvVar(environ, "VARNISH_LICENSE", "/new/path")

	want := []string{"FOO=bar", "VARNISH_LICENSE=/new/path"}
	if !reflect.DeepEqual(environ, want) {
		t.Errorf("setEnvVar did not replace in place, got %v, want %v", environ, want)
	}

	environ = setEnvVar(environ, "NEW_VAR", "value")
	want = []string{"FOO=bar", "VARNISH_LICENSE=/new/path", "NEW_VAR=value"}
	if !reflect.DeepEqual(environ, want) {
		t.Errorf("setEnvVar did not append new key, got %v, want %v", environ, want)
	}
}

func TestHasEnvVar(t *testing.T) {
	environ := []string{"FOO=bar", "VARNISH_LICENSE=/old/path"}

	if !hasEnvVar(environ, "VARNISH_LICENSE") {
		t.Error("expected VARNISH_LICENSE to be found")
	}
	if hasEnvVar(environ, "MISSING") {
		t.Error("expected MISSING to not be found")
	}
}
