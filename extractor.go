package grab

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/hydrz/grab/utils"
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
	Header   http.Header       // Custom headers for this stream (optional)
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

func (m *Media) String() string {
	output := strings.Builder{}
	output.WriteString(fmt.Sprintf("Title: %s\n", m.Title))
	if m.Description != "" {
		output.WriteString(fmt.Sprintf("Description: %s\n", m.Description))
	}
	if m.Thumbnail != "" {
		output.WriteString(fmt.Sprintf("Thumbnail: %s\n", m.Thumbnail))
	}
	output.WriteString("Streams:\n")
	if len(m.Streams) == 0 {
		output.WriteString("  No streams available.\n")
	} else {
		for _, stream := range m.Streams {
			output.WriteString(fmt.Sprintf("  [%s] %s\n", stream.ID, stream.Title))
			if stream.Quality != "" {
				output.WriteString(fmt.Sprintf("    Quality: %s\n", stream.Quality))
			}
			if stream.Type != "" {
				output.WriteString(fmt.Sprintf("    Type: %s\n", stream.Type))
			}
			if stream.Format != "" {
				output.WriteString(fmt.Sprintf("    Format: %s\n", stream.Format))
			}
			if stream.Size > 0 {
				output.WriteString(fmt.Sprintf("    Size: %s\n", utils.FormatBytes(stream.Size)))
			}
			if stream.Duration > 0 {
				output.WriteString(fmt.Sprintf("    Duration: %s\n", utils.FormatDuration(stream.Duration)))
			}
			if stream.URL != "" {
				output.WriteString(fmt.Sprintf("    URL: %s\n", stream.URL))
			}
			if stream.SaveAs != "" {
				output.WriteString(fmt.Sprintf("    Save As: %s\n", stream.SaveAs))
			}
			if len(stream.Extra) > 0 {
				output.WriteString(fmt.Sprintf("    Extra: %v\n", stream.Extra))
			}
			output.WriteString("\n")
		}
	}
	return output.String()
}

type extractorFactory func(ctx *Context) Extractor

// Extractor defines the interface for media extractors.
type Extractor interface {
	CanExtract(url string) bool
	Extract(url string) ([]Media, error)
}

var extractors = make(map[string]extractorFactory)
var lock sync.RWMutex

// Register registers an extractor factory for internal use.
func Register(name string, f extractorFactory) {
	lock.Lock()
	defer lock.Unlock()
	extractors[name] = f
}

// FindExtractor finds a suitable extractor for the given URL.
func FindExtractor(ctx *Context, url string) (Extractor, error) {
	lock.RLock()
	defer lock.RUnlock()
	for name, factory := range extractors {
		extractor := factory(ctx)
		if extractor.CanExtract(url) {
			ctx.logger.Debug("Using extractor", "name", name, "url", url)
			return extractor, nil
		}
	}
	return nil, ErrNoExtractorFound
}

// ListExtractors returns the names of all registered extractors.
func ListExtractors() []string {
	lock.RLock()
	defer lock.RUnlock()
	names := make([]string, 0, len(extractors))
	for name := range extractors {
		names = append(names, name)
	}
	return names
}
