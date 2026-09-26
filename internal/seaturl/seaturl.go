// Package seaturl encodes a seat preference into provider booking URLs that
// accept one. Kiwi uses seat. KLM uses seatPreference. Other hosts are unchanged.
package seaturl

import "net/url"

// Apply adds the seat preference to a Kiwi or KLM booking URL.
// pref is "window", "aisle", or "no_preference". An empty preference, an
// unknown preference, or any other host returns raw unchanged.
func Apply(raw, pref string) string {
	pref = canonical(pref)
	if pref == "" || raw == "" {
		return raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return raw
	}
	key := paramKey(u.Host)
	if key == "" {
		return raw
	}
	q := u.Query()
	q.Set(key, pref)
	u.RawQuery = q.Encode()
	return u.String()
}

func canonical(pref string) string {
	switch pref {
	case "window", "aisle":
		return pref
	default:
		return ""
	}
}

func paramKey(host string) string {
	switch {
	case host == "kiwi.com" || host == "www.kiwi.com" || hasSuffix(host, ".kiwi.com"):
		return "seat"
	case host == "klm.com" || host == "www.klm.com" || hasSuffix(host, ".klm.com"):
		return "seatPreference"
	default:
		return ""
	}
}

func hasSuffix(s, suffix string) bool {
	return len(s) >= len(suffix) && s[len(s)-len(suffix):] == suffix
}
