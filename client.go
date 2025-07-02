package grab

import (
	"strings"
	"time"

	"github.com/go-resty/resty/v2"
	"github.com/hydrz/grab/utils"
)

// newClient creates a configured resty client with robust error handling and caching.
// newClient creates a configured resty client with robust error handling, caching, and advanced authentication.
func newClient(o Option) *resty.Client {
	client := resty.New()

	// Set reasonable defaults
	if o.Timeout > 0 {
		client.SetTimeout(o.Timeout)
	} else {
		client.SetTimeout(30 * time.Second) // Default 30 second timeout
	}

	// Set proxy if configured
	if o.Proxy != "" {
		client.SetProxy(o.Proxy)
	}

	// Authentication setup
	if o.AuthUser != "" && o.AuthPass != "" {
		client.SetBasicAuth(o.AuthUser, o.AuthPass)
	}
	if o.AuthToken != "" {
		o.Headers.Set("Authorization", "Bearer "+o.AuthToken)
	}
	if o.AuthHeader != "" {
		// Format: "Key: Value"
		parts := strings.SplitN(o.AuthHeader, ":", 2)
		if len(parts) == 2 {
			o.Headers.Set(strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]))
		}
	}

	// Load cookies from file if specified
	if o.Cookie != "" {
		cookieJar, err := utils.CookieJarFromFile(o.Cookie)
		if err != nil {
			panic("Failed to load cookie file: " + o.Cookie)
		}
		client.SetCookieJar(cookieJar)
	}

	// Configure retry behavior
	if o.RetryCount > 0 {
		client.SetRetryCount(o.RetryCount)
		client.SetRetryWaitTime(3 * time.Second)
		client.SetRetryMaxWaitTime(30 * time.Second)

		// Add retry conditions - don't retry on 4xx errors except specific ones
		client.AddRetryCondition(func(r *resty.Response, _ error) bool {
			// Don't retry on client errors except for specific cases
			if r.StatusCode() >= 400 && r.StatusCode() < 500 {
				// Retry on rate limiting and some temporary client errors
				switch r.StatusCode() {
				case 408, 429: // Request Timeout, Too Many Requests
					return true
				default:
					return false
				}
			}

			// Don't retry on 304 Not Modified - it's not an error
			if r.StatusCode() == 304 {
				return false
			}

			// Retry on 5xx server errors
			return r.StatusCode() >= 500
		})
	}

	// Set custom headers
	if o.Headers != nil {
		client.Header = o.Headers.Clone()
	}

	// Set user agent
	userAgent := o.UserAgent
	if userAgent == "" {
		userAgent = defaultUserAgent
	}
	client.SetHeader("User-Agent", userAgent)

	// Disable debug by default, enable only if explicitly requested
	if o.Debug {
		client.SetDebug(true)
	}

	// Set additional headers for better compatibility
	client.SetHeader("Accept", "*/*")
	client.SetHeader("Accept-Encoding", "gzip, deflate")
	client.SetHeader("Connection", "keep-alive")

	return client
}
