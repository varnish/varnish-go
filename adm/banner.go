package adm

import (
	"context"
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
// The result is cached after the first successful call.
func (c *Conn) Version(ctx context.Context) (BannerVersion, error) {
	if c.cachedVersion != nil {
		return *c.cachedVersion, nil
	}
	msg, err := c.Ask(ctx, "banner")
	if err != nil {
		return BannerVersion{}, err
	}
	for _, line := range strings.Split(msg, "\n") {
		m := bannerVersionRE.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		v := BannerVersion{
			IsEnterprise: m[1] == "varnish-plus",
			Version:      m[2],
			Revision:     m[3],
		}
		c.cachedVersion = &v
		return v, nil
	}
	return BannerVersion{}, fmt.Errorf("adm.Version: no version line found in banner")
}
