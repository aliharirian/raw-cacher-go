package cache

import "time"

type Meta struct {
	ETag         string `json:"etag,omitempty"`
	LastModified string `json:"last_modified,omitempty"`
	CachedAt     string `json:"cached_at,omitempty"`
	TTL          int    `json:"ttl_sec,omitempty"`
	Size         int64  `json:"size,omitempty"`
	Neg          bool   `json:"neg,omitempty"`
}

func NowISO() string { return time.Now().UTC().Format(time.RFC3339Nano) }

func IsFresh(m Meta, defaultTTL int) bool {
	if m.Neg {
		return false
	}
	ttl := m.TTL
	if ttl <= 0 {
		ttl = defaultTTL
	}
	if m.CachedAt == "" {
		return false
	}
	t, err := time.Parse(time.RFC3339Nano, m.CachedAt)
	if err != nil {
		return false
	}
	return time.Since(t) < time.Duration(ttl)*time.Second
}

func IsNegativeFresh(m Meta, ttl404 int) bool {
	if !m.Neg || m.CachedAt == "" {
		return false
	}
	t, err := time.Parse(time.RFC3339Nano, m.CachedAt)
	if err != nil {
		return false
	}
	ttl := m.TTL
	if ttl <= 0 {
		ttl = ttl404
	}
	return time.Since(t) < time.Duration(ttl)*time.Second
}

func ObjectKey(domain, route string) string {
	for len(route) > 0 && route[0] == '/' {
		route = route[1:]
	}
	return "objects/" + domain + "/" + route
}

func MetaKey(domain, route string) string {
	for len(route) > 0 && route[0] == '/' {
		route = route[1:]
	}
	return "meta/" + domain + "/" + route + ".json"
}
