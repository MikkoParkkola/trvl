package hotels

import "github.com/MikkoParkkola/trvl/internal/providers"

func init() {
	browserCookies = providers.BrowserCookiesForURL
}
