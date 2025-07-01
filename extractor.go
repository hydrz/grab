package grab

import (
	"net/http"
	"sync"
	"time"
)

// StreamType defines the type of media resource.
type StreamType string

const (
	StreamTypeVideo    StreamType = "video"
	StreamTypeAudio    StreamType = "audio"
	StreamTypeImage    StreamType = "image"
	StreamTypeSubtitle StreamType = "subtitle"
	StreamTypePlaylist StreamType = "playlist"
	StreamTypeM3u8     StreamType = "m3u8"
	StreamTypeDocument StreamType = "document"
	StreamTypeOther    StreamType = "other"
)

// Stream represents a single media stream (e.g. one quality/format)
type Stream struct {
	ID       string            // Unique identifier for this stream
	Title    string            // Title or name of the stream
	Type     StreamType        // Type of the stream (video, audio, etc.)
	URL      string            // Direct URL to this stream
	Format   string            // Format (e.g., "mp4", "webm", "mp3")
	Quality  string            // Quality/bitrate info (e.g., "1080p", "320kbps")
	Size     int64             // Size in bytes (if known)
	Duration time.Duration     // Duration of the stream (if applicable)
	Headers  http.Header       // Custom headers for this stream (optional)
	Extra    map[string]string // Extensible fields (e.g., codec info)
	SaveAs   string            // Suggested filename to save this stream
}

// Media represents a downloadable media resource with multiple streams.
type Media struct {
	Title       string            // Media title or name
	Streams     []Stream          // All available streams (keyed by quality or id)
	Thumbnail   string            // URL to thumbnail image (optional)
	Description string            // Description of the media (optional)
	Extra       map[string]string // Additional info for extensibility
}

type Extractor interface {
	Name() string
	CanExtract(url string) bool
	Extract(url string, options Option) ([]Media, error)
}

var extractors = make(map[string]Extractor)
var lock sync.RWMutex

func Register(extractor Extractor) {
	lock.Lock()
	defer lock.Unlock()
	if extractor == nil {
		panic("grab: Register extractor is nil")
	}
	extractors[extractor.Name()] = extractor
}

func FindExtractor(url string) (Extractor, error) {
	lock.RLock()
	defer lock.RUnlock()
	for _, extractor := range extractors {
		if extractor.CanExtract(url) {
			return extractor, nil
		}
	}
	return nil, ErrNoExtractorFound
}

func ListExtractors() []string {
	lock.RLock()
	defer lock.RUnlock()
	names := make([]string, 0, len(extractors))
	for name := range extractors {
		names = append(names, name)
	}
	return names
}
