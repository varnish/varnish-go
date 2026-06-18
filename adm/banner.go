package adm

import (
	"fmt"
	"regexp"
	"strings"
)

// BannerVersion holds version information extracted from the Varnish admin banner.
type BannerVersion struct {
	IsEnterprise bool   // true for Varnish Enterprise, false for Varnish Cache
	Version      string // e.g. "9.0.3" or "6.0.17r4"
	Revision     string // git commit hash
}

// bannerVersionRE matches the version line in the admin banner:
//
//	varnish-9.0.3 revision <hash>        (Varnish Cache)
//	varnish-plus-6.0.17r4 revision <hash> (Varnish Enterprise)
var bannerVersionRE = regexp.MustCompile(`^(varnish(?:-plus)?)-(\S+)\s+revision\s+(\S+)`)

// Version returns version information from the Varnish admin banner.
func (c *Conn) Version() (BannerVersion, error) {
	msg, err := c.Ask("banner")
	if err != nil {
		return BannerVersion{}, err
	}
	for _, line := range strings.Split(msg, "\n") {
		m := bannerVersionRE.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		return BannerVersion{
			IsEnterprise: m[1] == "varnish-plus",
			Version:      m[2],
			Revision:     m[3],
		}, nil
	}
	return BannerVersion{}, fmt.Errorf("adm.Version: no version line found in banner")
}
