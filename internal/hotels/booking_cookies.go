package hotels

import (
	"context"
	"net/http"

	"github.com/browserutils/kooky"
	_ "github.com/browserutils/kooky/browser/all"
)

func init() {
	browserCookies = readBookingCookiesFromKooky
}

func readBookingCookiesFromKooky(targetURL string) []*http.Cookie {
	cookies, err := kooky.ReadCookies(context.Background(),
		kooky.Valid,
		kooky.DomainContains("booking.com"),
	)
	if err != nil || len(cookies) == 0 {
		return nil
	}
	result := make([]*http.Cookie, 0, len(cookies))
	for _, c := range cookies {
		if c == nil {
			continue
		}
		hc := &http.Cookie{
			Name:     c.Name,
			Value:    c.Value,
			Domain:   c.Domain,
			Path:     c.Path,
			Expires:  c.Expires,
			Secure:   c.Secure,
			HttpOnly: c.HttpOnly,
		}
		result = append(result, hc)
	}
	return result
}
