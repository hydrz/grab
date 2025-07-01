package grab

import "sync"

// MediaType defines the type of media resource.
type MediaType string

const (
	MediaTypeVideo MediaType = "video"
	MediaTypeAudio MediaType = "audio"
	MediaTypeImage MediaType = "image"
	MediaTypeOther MediaType = "other"
)

// Stream represents a single media stream (e.g. one quality/format)
type Stream struct {
	URL      string            // Direct URL to this stream
	Format   string            // Format (e.g., "mp4", "webm", "mp3")
	Quality  string            // Quality/bitrate info (e.g., "1080p", "320kbps")
	Size     int64             // Size in bytes (if known)
	Duration float64           // Duration in seconds (if known)
	Headers  map[string]string // Custom headers for this stream (optional)
	Extra    map[string]string // Extensible fields (e.g., codec info)
	SaveAs   string            // Suggested filename to save this stream
}

// Media represents a downloadable media resource with multiple streams.
type Media struct {
	Title     string            // Media title or name
	Type      MediaType         // Media type: video, audio, image, etc.
	Streams   map[string]Stream // All available streams (keyed by quality or id)
	Subtitles []string          // URLs to subtitle files (optional)
	Extra     map[string]string // Additional info for extensibility
}

type Extractor interface {
	Name() string
	CanExtract(url string) bool
	Extract(url string, options *Option) ([]Media, error)
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
