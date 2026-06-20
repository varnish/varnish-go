package adm

import (
	"context"
	"encoding/json"
	"math"
	"time"
)

// BanEntry describes a single active ban in the ban list.
type BanEntry struct {
	Time      time.Time // when the ban was issued
	Refs      int       `json:"refs"`      // number of objects currently tested against this ban
	Completed bool      `json:"completed"` // true if no further objects need testing
	Spec      string    `json:"spec"`      // the ban expression as originally submitted
}

type banEntryRaw struct {
	Time      float64 `json:"time"`
	Refs      int     `json:"refs"`
	Completed bool    `json:"completed"`
	Spec      string  `json:"spec"`
}

func (e *BanEntry) UnmarshalJSON(data []byte) error {
	var raw banEntryRaw
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	sec := int64(raw.Time)
	nsec := int64(math.Round((raw.Time - float64(sec)) * 1e9))
	e.Time = time.Unix(sec, nsec)
	e.Refs = raw.Refs
	e.Completed = raw.Completed
	e.Spec = raw.Spec
	return nil
}

// BanList returns all active bans.
func (c *Conn) BanList(ctx context.Context) ([]BanEntry, error) {
	msg, err := c.Ask(ctx, "ban.list", "-j")
	if err != nil {
		return nil, err
	}
	return parseJSONItems[BanEntry](msg)
}

// Ban creates a new ban matching the given expression.
// expression format: "field operator arg [&& field operator arg ...]"
func (c *Conn) Ban(ctx context.Context, expression string) error {
	_, err := c.Ask(ctx, "ban", expression)
	return err
}
