package grab

import (
	"github.com/go-resty/resty/v2"
	"github.com/hydrz/grab/utils"
)

func (o *Option) Client() *resty.Client {
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

	return client
}
