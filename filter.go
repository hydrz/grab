package grab

import (
	"sort"
	"strconv"
	"strings"
)

// Filter defines the interface for stream filtering.
type Filter interface {
	Filter(stream Stream) bool
}

// QualityFilter filters streams by quality.
// Supports "best" (default, highest quality), "worst" (lowest quality), or exact match.
type QualityFilter string

func (q QualityFilter) Filter(stream Stream) bool {
	// "best" and "worst" handled in FiltersForStreams, here only exact match
	return q == "" || strings.EqualFold(stream.Quality, string(q))
}

// VideoOnlyFilter filters only video or m3u8 streams.
type VideoOnlyFilter struct{}

func (f *VideoOnlyFilter) Filter(stream Stream) bool {
	return stream.Type == StreamTypeVideo || stream.Type == StreamTypeM3u8
}

// AudioOnlyFilter filters only audio streams.
type AudioOnlyFilter struct{}

func (f *AudioOnlyFilter) Filter(stream Stream) bool {
	return stream.Type == StreamTypeAudio
}

// NoSubtitleFilter filters out subtitle streams.
type NoSubtitleFilter struct{}

func (f *NoSubtitleFilter) Filter(stream Stream) bool {
	return stream.Type != StreamTypeSubtitle
}

// PlaylistFilter filters playlist streams by index range.
type PlaylistFilter struct {
	Start int
	End   int
}

func (f *PlaylistFilter) Filter(stream Stream) bool {
	if stream.Type != StreamTypePlaylist {
		return true // Not a playlist, so pass through
	}
	id, err := strconv.Atoi(stream.ID)
	if err != nil {
		return false
	}
	if f.Start < 0 && f.End < 0 {
		return true // No limits set, pass through
	}
	if f.Start >= 0 && f.End >= 0 {
		return id >= f.Start && id <= f.End
	}
	if f.Start >= 0 {
		return id >= f.Start
	}
	if f.End >= 0 {
		return id <= f.End
	}
	return false
}

// FiltersForStreams returns a list of filters based on Option.
// If Quality is "best" or empty, will only keep the highest quality stream.
func (o *Option) FiltersForStreams(streams []Stream) []Filter {
	var filters []Filter
	quality := o.Quality
	if quality == "" {
		quality = "best"
	}

	if quality == "best" || quality == "worst" {
		// Find all unique qualities and sort
		qualities := make(map[string]int)
		order := []string{}
		for i, s := range streams {
			if _, ok := qualities[s.Quality]; !ok {
				qualities[s.Quality] = i
				order = append(order, s.Quality)
			}
		}

		// Sort qualities by a custom rule, e.g., resolution or bitrate
		sort.Slice(order, func(i, j int) bool {
			// Try to parse as int, fallback to string compare
			qi, erri := strconv.Atoi(order[i])
			qj, errj := strconv.Atoi(order[j])
			if erri == nil && errj == nil {
				return qi > qj // higher is better
			}
			return order[i] > order[j]
		})
		var target string
		if quality == "best" {
			target = order[0]
		} else {
			target = order[len(order)-1]
		}
		filters = append(filters, QualityFilter(target))
	} else if quality != "" {
		filters = append(filters, QualityFilter(quality))
	}
	if o.VideoOnly {
		filters = append(filters, &VideoOnlyFilter{})
	}
	if o.AudioOnly {
		filters = append(filters, &AudioOnlyFilter{})
	}
	if !o.Subtitle {
		filters = append(filters, &NoSubtitleFilter{})
	}
	if o.PlaylistStart > 0 || o.PlaylistEnd > 0 {
		filters = append(filters, &PlaylistFilter{
			Start: o.PlaylistStart,
			End:   o.PlaylistEnd,
		})
	}
	return filters
}
