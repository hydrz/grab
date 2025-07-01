package grab

import "time"

// Option contains configuration options for downloading
type Option struct {
	// OutputPath specifies the directory to save downloaded files
	OutputPath string

	// OutputName specifies the filename for the downloaded file
	OutputName string

	// Quality specifies preferred quality (e.g., "720p", "480p", "best", "worst")
	Quality string

	// Format specifies preferred format (e.g., "mp4", "mp3", "auto")
	Format string

	// Cookie specifies the cookie file path for authentication
	Cookie string

	// Headers specifies custom HTTP headers
	Headers map[string]string

	// UserAgent specifies custom user agent
	UserAgent string

	// Proxy specifies proxy URL (e.g., "http://127.0.0.1:8080")
	Proxy string

	// RetryCount specifies number of retry attempts
	RetryCount int

	// Timeout specifies request timeout in seconds
	Timeout time.Duration

	// Threads specifies number of concurrent download threads
	Threads int

	// ChunkSize specifies download chunk size in bytes
	ChunkSize int64

	// SkipExisting skips download if file already exists
	SkipExisting bool

	// ExtractOnly only extracts media info without downloading
	ExtractOnly bool

	// Playlist downloads all videos in playlist
	Playlist bool

	// PlaylistStart specifies starting index for playlist download
	PlaylistStart int

	// PlaylistEnd specifies ending index for playlist download
	PlaylistEnd int

	// Subtitle downloads subtitle files
	Subtitle bool

	// AudioOnly downloads audio only
	AudioOnly bool

	// VideoOnly downloads video only (no audio)
	VideoOnly bool

	// IgnoreErrors continues downloading even if some items fail
	IgnoreErrors bool

	// Debug enables debug logging
	Debug bool

	// Verbose enables verbose output
	Verbose bool

	// Silent suppresses all output except errors
	Silent bool
}
