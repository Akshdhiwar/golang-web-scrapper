// scrapers/http.go
package scrapers

import (
	"net/http"
	"time"
)

// httpClient is shared by the plain-HTTP scrapers so connections (and TLS
// sessions) are reused across tasks.
var httpClient = &http.Client{
	Timeout: 20 * time.Second,
	Transport: &http.Transport{
		Proxy:               http.ProxyFromEnvironment,
		MaxIdleConnsPerHost: 8,
		IdleConnTimeout:     90 * time.Second,
		ForceAttemptHTTP2:   true,
	},
}
