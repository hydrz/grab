package grab

import (
	"os"
	"path/filepath"

	"github.com/go-resty/resty/v2"
	"github.com/gregjones/httpcache"
	"github.com/gregjones/httpcache/diskcache"
	"github.com/hydrz/grab/utils"
)

func NewClient(o Option) *resty.Client {
	client := resty.New()

	if o.Timeout > 0 {
		client.SetTimeout(o.Timeout)
	}

	if o.Proxy != "" {
		client.SetProxy(o.Proxy)
	}

	if o.Cookie != "" {
		cookieJar, err := utils.CookieJarFromFile(o.Cookie)
		if err != nil {
			panic("Failed to load cookie file: " + o.Cookie)
		}
		client.SetCookieJar(cookieJar)
	}

	if o.RetryCount > 0 {
		client.SetRetryCount(o.RetryCount)
	}

	if o.Headers != nil {
		client.Header = o.Headers.Clone()
	}

	if o.UserAgent != "" {
		client.SetHeader("User-Agent", o.UserAgent)
	}

	if o.Debug {
		client.SetDebug(true)
	}

	if !o.NoCache {
		cachePath := filepath.Join(os.TempDir(), "grab_cache")
		cache := diskcache.New(cachePath)
		transport := httpcache.NewTransport(cache)
		client.SetTransport(transport)
	}

	return client
}
