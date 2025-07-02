package grab

import (
	"net/http"
	"runtime"
	"time"

	"github.com/hydrz/grab/utils"
)

const defaultUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"

// Option defines all configurable parameters for downloading and extraction.
// Each field corresponds to a command-line flag in main.go/setupFlags.
// Callers should ensure OutputPath exists or is creatable.
// Threads should be >=1; ChunkSize in bytes.
type Option struct {
	OutputPath    string        // Output directory for downloaded files (--output-dir, -o)
	OutputName    string        // Output filename (--output-filename, -O)
	Quality       string        // Preferred video quality, e.g. "best", "worst", "720p" (--quality, -q)
	Format        string        // Output format, e.g. "mp4", "mkv", "mp3" (--format, -f)
	Cookie        string        // Cookie file path for authentication (--cookies, -c)
	Headers       http.Header   // Custom HTTP headers (--header, -H)
	UserAgent     string        // Custom user agent (--user-agent, -u)
	Proxy         string        // HTTP proxy URL (--proxy, -x)
	RetryCount    int           // Number of retry attempts (--retry, -r)
	Timeout       time.Duration // Request timeout (--timeout, -t)
	Threads       int           // Number of concurrent download threads (--threads, -n)
	ChunkSize     int64         // Download chunk size in bytes
	SkipExisting  bool          // Skip download if file already exists (--skip, -s)
	ExtractOnly   bool          // Only extract media info, do not download (--info, -i)
	Playlist      bool          // Download all videos in playlist (--playlist, -p)
	PlaylistStart int           // Playlist start index (--playlist-start)
	PlaylistEnd   int           // Playlist end index (--playlist-end)
	Subtitle      bool          // Download subtitles (--subtitle)
	VideoOnly     bool          // Download video only, no audio (--video-only)
	AudioOnly     bool          // Download audio only (--audio-only)
	IgnoreErrors  bool          // Continue on errors (--ignore-errors)
	Debug         bool          // Enable debug logging (--debug, -d)
	Verbose       bool          // Enable verbose output (--verbose, -v)
	Silent        bool          // Suppress all output except errors (--silent)
	NoCache       bool          // Disable caching (--no-cache)
}

func (o *Option) Combine(other Option) {
	if other.OutputPath != "" {
		o.OutputPath = other.OutputPath
	}
	if other.OutputName != "" {
		o.OutputName = other.OutputName
	}
	if other.Quality != "" {
		o.Quality = other.Quality
	}
	if other.Format != "" {
		o.Format = other.Format
	}
	if other.Cookie != "" {
		o.Cookie = other.Cookie
	}
	if len(other.Headers) > 0 {
		o.Headers = utils.MergeHeader(o.Headers, other.Headers)
	}
	if other.UserAgent != "" {
		o.UserAgent = other.UserAgent
	}
	if other.Proxy != "" {
		o.Proxy = other.Proxy
	}
	if other.RetryCount > 0 {
		o.RetryCount = other.RetryCount
	}
	if other.Timeout > 0 {
		o.Timeout = other.Timeout
	}
	if other.Threads > 0 {
		o.Threads = other.Threads
	}
	if other.ChunkSize > 0 {
		o.ChunkSize = other.ChunkSize
	}

	o.SkipExisting = o.SkipExisting || other.SkipExisting
	o.ExtractOnly = o.ExtractOnly || other.ExtractOnly

	o.Playlist = o.Playlist || other.Playlist
	if other.PlaylistStart > 0 {
		o.PlaylistStart = other.PlaylistStart
	}
	if other.PlaylistEnd > 0 {
		o.PlaylistEnd = other.PlaylistEnd
	}

	o.Subtitle = o.Subtitle || other.Subtitle
	o.VideoOnly = o.VideoOnly || other.VideoOnly
	o.AudioOnly = o.AudioOnly || other.AudioOnly
	o.IgnoreErrors = o.IgnoreErrors || other.IgnoreErrors
	o.Debug = o.Debug || other.Debug
	o.Verbose = o.Verbose || other.Verbose
	o.Silent = o.Silent || other.Silent
	o.NoCache = o.NoCache || other.NoCache
}

var DefaultOptions = &Option{
	OutputPath: "./downloads",
	RetryCount: 3,
	Timeout:    30 * time.Second,
	Threads:    max(4, runtime.NumCPU()), // Use at least 4 threads or number of CPU cores
	ChunkSize:  1024 * 1024,              // 1 MB
	UserAgent:  defaultUserAgent,
}
