package grab

import (
	"io"
	"sync/atomic"
	"time"
)

// ProgressCallback defines the callback function for progress updates
type ProgressCallback func(current, total int64, description string)

// progress tracks progress in concurrent environments with callback support.
type progress struct {
	Total       int64
	Current     atomic.Int64
	Description string
	callback    ProgressCallback
	lastUpdate  atomic.Int64
}

func newProgress(total int64, description string) *progress {
	if total < 0 {
		total = 0
	}
	return &progress{
		Total:       total,
		Description: description,
	}
}

// SetCallback sets the progress callback function
func (p *progress) SetCallback(callback ProgressCallback) {
	p.callback = callback
}

// Add increments the progress bar by the specified amount.
func (p *progress) Add(num int64) {
	if num < 0 {
		num = 0
	}
	current := p.Current.Add(num)

	// Rate limit callback calls to avoid performance issues
	if p.callback != nil {
		now := time.Now().UnixMilli()
		lastUpdate := p.lastUpdate.Load()
		if now-lastUpdate > 100 { // Update at most every 100ms
			if p.lastUpdate.CompareAndSwap(lastUpdate, now) {
				p.callback(current, p.Total, p.Description)
			}
		}
	}
}

// Finish marks the progress bar as finished.
func (p *progress) Finish() {
	p.Current.Store(p.Total)
	if p.callback != nil {
		p.callback(p.Total, p.Total, p.Description)
	}
}

func (p *progress) NewReader(r io.Reader) io.ReadCloser {
	return &progressReader{Reader: r, bar: p}
}

// progressReader wraps an io.progressReader and updates the progress bar as data is read.
type progressReader struct {
	io.Reader
	bar *progress
}

// Read reads data and updates the progress bar.
func (r *progressReader) Read(p []byte) (n int, err error) {
	n, err = r.Reader.Read(p)
	r.bar.Add(int64(n))
	return
}

// Close closes the underlying reader if it implements io.Closer, otherwise finishes the progress bar.
func (r *progressReader) Close() error {
	if closer, ok := r.Reader.(io.Closer); ok {
		_ = closer.Close()
	}
	r.bar.Finish()
	return nil
}
