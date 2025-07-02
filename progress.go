package grab

import (
	"io"
	"sync/atomic"
	"time"
)

// ProgressCallback defines the callback function for progress updates
type ProgressCallback func(current, total int64, description string)

// Progress tracks progress in concurrent environments with callback support.
type Progress struct {
	Total       int64
	Current     atomic.Int64
	Description string
	callback    ProgressCallback
	lastUpdate  atomic.Int64
}

func NewProgress(total int64, description string) *Progress {
	if total < 0 {
		total = 0
	}
	return &Progress{
		Total:       total,
		Description: description,
	}
}

// SetCallback sets the progress callback function
func (p *Progress) SetCallback(callback ProgressCallback) {
	p.callback = callback
}

// Add increments the progress bar by the specified amount.
func (p *Progress) Add(num int64) {
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
func (p *Progress) Finish() {
	p.Current.Store(p.Total)
	if p.callback != nil {
		p.callback(p.Total, p.Total, p.Description)
	}
}

func (p *Progress) NewReader(r io.Reader) io.ReadCloser {
	return &Reader{Reader: r, bar: p}
}

// Reader wraps an io.Reader and updates the progress bar as data is read.
type Reader struct {
	io.Reader
	bar *Progress
}

// Read reads data and updates the progress bar.
func (r *Reader) Read(p []byte) (n int, err error) {
	n, err = r.Reader.Read(p)
	r.bar.Add(int64(n))
	return
}

// Close closes the underlying reader if it implements io.Closer, otherwise finishes the progress bar.
func (r *Reader) Close() error {
	if closer, ok := r.Reader.(io.Closer); ok {
		_ = closer.Close()
	}
	r.bar.Finish()
	return nil
}
