package utils

import (
	"bufio"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// CookieJarFromFile loads cookies from Mozilla cookies.sqlite or Netscape cookies.txt
func CookieJarFromFile(filePath string) (*cookiejar.Jar, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create cookie jar: %w", err)
	}

	// Check file extension to determine format
	if strings.HasSuffix(strings.ToLower(filePath), ".txt") {
		return loadNetscapeCookies(filePath, jar)
	}

	// For SQLite files, we would need additional handling
	// For now, return error for unsupported formats
	return nil, fmt.Errorf("unsupported cookie file format: %s", filePath)
}

// loadNetscapeCookies loads cookies from Netscape format (cookies.txt)
func loadNetscapeCookies(filePath string, jar *cookiejar.Jar) (*cookiejar.Jar, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open cookie file: %w", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// Skip comments and empty lines
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Parse Netscape cookie format
		parts := strings.Split(line, "\t")
		if len(parts) < 7 {
			continue
		}

		domain := parts[0]
		flag := parts[1] == "TRUE"
		path := parts[2]
		secure := parts[3] == "TRUE"
		expirationStr := parts[4]
		name := parts[5]
		value := parts[6]

		// Parse expiration time
		var expiration time.Time
		if expirationStr != "0" {
			if exp, err := strconv.ParseInt(expirationStr, 10, 64); err == nil {
				expiration = time.Unix(exp, 0)
			}
		}

		// Create cookie
		cookie := &http.Cookie{
			Name:     name,
			Value:    value,
			Path:     path,
			Domain:   domain,
			Expires:  expiration,
			Secure:   secure,
			HttpOnly: flag,
		}

		// Create URL for this domain
		scheme := "http"
		if secure {
			scheme = "https"
		}
		u, err := url.Parse(fmt.Sprintf("%s://%s%s", scheme, domain, path))
		if err != nil {
			continue
		}

		// Add cookie to jar
		jar.SetCookies(u, []*http.Cookie{cookie})
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("failed to read cookie file: %w", err)
	}

	return jar, nil
}
