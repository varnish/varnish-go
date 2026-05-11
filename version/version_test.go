package version

import "testing"

func TestParse(t *testing.T) {
	cases := []struct {
		input      string
		enterprise bool
		version    string
		commit     string
	}{
		{
			"Varnish Plus 6.0.17r3 dcfc3d805386e802d41bde05cd9f113a74a216f4",
			true, "6.0.17r3", "dcfc3d805386e802d41bde05cd9f113a74a216f4",
		},
		{
			"Varnish 9.0.0 ce1b315b0c35477c666e4c8d8e1c9174df87eb61",
			false, "9.0.0", "ce1b315b0c35477c666e4c8d8e1c9174df87eb61",
		},
	}

	for _, c := range cases {
		e, v, r := parse(c.input)
		if e != c.enterprise || v != c.version || r != c.commit {
			t.Errorf("parse(%q) = (%v, %q, %q), want (%v, %q, %q)",
				c.input, e, v, r, c.enterprise, c.version, c.commit)
		}
	}
}
