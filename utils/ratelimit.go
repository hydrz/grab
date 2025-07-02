package utils

import (
	"io"
	"time"
)

// RateLimiter wraps an io.Reader and limits the read speed to the specified bytes per second.
// RateLimiter implements io.ReadCloser for compatibility with io.ReadCloser chains.
type RateLimiter struct {
	io.Reader
	closer    io.Closer
	Rate      int64         // bytes per second
	interval  time.Duration // sleep interval
	chunkSize int           // bytes per interval
}

// NewRateLimiter creates a new RateLimiter for the given reader and rate (bytes/sec).
func NewRateLimiter(r io.Reader, rate int64) *RateLimiter {
	var closer io.Closer
	if c, ok := r.(io.Closer); ok {
		closer = c
	}
	if rate <= 0 {
		return &RateLimiter{Reader: r, closer: closer}
	}
	interval := 100 * time.Millisecond
	chunkSize := int(rate / 10)
	if chunkSize < 1 {
		chunkSize = 1
	}
	return &RateLimiter{
		Reader:    r,
		closer:    closer,
		Rate:      rate,
		interval:  interval,
		chunkSize: chunkSize,
	}
}

// Read reads data from the underlying reader, limiting the speed.
func (rl *RateLimiter) Read(p []byte) (int, error) {
	if rl.Rate <= 0 {
		return rl.Reader.Read(p)
	}
	max := rl.chunkSize
	if len(p) < max {
		max = len(p)
	}
	n, err := rl.Reader.Read(p[:max])
	if n > 0 {
		time.Sleep(rl.interval)
	}
	return n, err
}

// Close closes the underlying reader if possible.
func (rl *RateLimiter) Close() error {
	if rl.closer != nil {
		return rl.closer.Close()
	}
	return nil
}
