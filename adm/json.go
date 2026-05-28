package adm

import (
	"encoding/json"
	"fmt"
)

// parseJSONItems unmarshals the data items from a varnishd -j response array.
// All -j responses have the form [version, [cmd, args...], timestamp, item, item, ...].
func parseJSONItems[T any](msg string) ([]T, error) {
	var raw []json.RawMessage
	if err := json.Unmarshal([]byte(msg), &raw); err != nil {
		return nil, err
	}
	if len(raw) < 3 {
		return nil, fmt.Errorf("unexpected response format: %s", msg)
	}
	var items []T
	for _, r := range raw[3:] {
		var item T
		if err := json.Unmarshal(r, &item); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}

// parseJSONSingle unmarshals the single data item from a varnishd -j response array.
func parseJSONSingle[T any](msg string) (T, error) {
	items, err := parseJSONItems[T](msg)
	if err != nil {
		var zero T
		return zero, err
	}
	if len(items) == 0 {
		var zero T
		return zero, fmt.Errorf("unexpected empty response")
	}
	return items[0], nil
}
