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
type qualityFilter string

func (q qualityFilter) Filter(stream Stream) bool {
	// "best" and "worst" handled in filtersForStreams, here only exact match
	return q == "" || strings.EqualFold(stream.Quality, string(q))
}

// VideoOnlyFilter filters only video or m3u8 streams.
type videoOnlyFilter struct{}

func (f *videoOnlyFilter) Filter(stream Stream) bool {
	return stream.Type == StreamTypeVideo || stream.Type == StreamTypeM3u8
}

// AudioOnlyFilter filters only audio streams.
type audioOnlyFilter struct{}

func (f *audioOnlyFilter) Filter(stream Stream) bool {
	return stream.Type == StreamTypeAudio
}

// NoSubtitleFilter filters out subtitle streams.
type noSubtitleFilter struct{}

func (f *noSubtitleFilter) Filter(stream Stream) bool {
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

// filtersForStreams returns a list of filters based on Option.
// If Quality is "best" or empty, will only keep the highest quality stream.
func (o *Option) filtersForStreams(streams []Stream) []Filter {
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
		filters = append(filters, qualityFilter(target))
	} else if quality != "" {
		filters = append(filters, qualityFilter(quality))
	}
	if o.VideoOnly {
		filters = append(filters, &videoOnlyFilter{})
	}
	if o.AudioOnly {
		filters = append(filters, &audioOnlyFilter{})
	}
	if !o.Subtitle {
		filters = append(filters, &noSubtitleFilter{})
	}
	if o.PlaylistStart > 0 || o.PlaylistEnd > 0 {
		filters = append(filters, &PlaylistFilter{
			Start: o.PlaylistStart,
			End:   o.PlaylistEnd,
		})
	}
	return filters
}
