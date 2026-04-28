package sfu

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// ParseICEServersJSON parses ICE_SERVERS_JSON into normalized ICE server configs.
// Supported format:
// [{"urls":["stun:host:3478","turn:host:3478"],"username":"user","credential":"pass"}]
func ParseICEServersJSON(raw string) ([]ICEServer, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}

	type payload struct {
		URLs       any    `json:"urls"`
		Username   string `json:"username"`
		Credential string `json:"credential"`
	}

	var items []payload
	if err := json.Unmarshal([]byte(raw), &items); err != nil {
		return nil, fmt.Errorf("parse ICE_SERVERS_JSON: %w", err)
	}

	servers := make([]ICEServer, 0, len(items))
	for _, item := range items {
		urls := normalizeICEURLs(item.URLs)
		if len(urls) == 0 {
			continue
		}
		servers = append(servers, ICEServer{
			URLs:       urls,
			Username:   strings.TrimSpace(item.Username),
			Credential: strings.TrimSpace(item.Credential),
		})
	}

	if len(servers) == 0 {
		return nil, errors.New("ICE_SERVERS_JSON must include at least one server with urls")
	}

	return servers, nil
}

func normalizeICEURLs(raw any) []string {
	switch value := raw.(type) {
	case string:
		value = strings.TrimSpace(value)
		if value == "" {
			return nil
		}
		return []string{value}
	case []any:
		urls := make([]string, 0, len(value))
		for _, item := range value {
			url, ok := item.(string)
			if !ok {
				continue
			}
			url = strings.TrimSpace(url)
			if url != "" {
				urls = append(urls, url)
			}
		}
		if len(urls) == 0 {
			return nil
		}
		return urls
	default:
		return nil
	}
}
