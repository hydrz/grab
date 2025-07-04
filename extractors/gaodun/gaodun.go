package gaodun

import (
	"fmt"
	"net/http"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hydrz/grab"
	"github.com/hydrz/grab/utils"
)

func init() {
	grab.Register("gaodun", func(ctx *grab.Context) grab.Extractor {
		return &extractor{
			ctx: ctx,
			api: NewApi(ctx.Client().Clone()),
		}
	})
}

// extractor implements grab.Extractor for Gaodun platform.
type extractor struct {
	ctx *grab.Context
	api Api
}

// Name returns the extractor's unique name.
func (e *extractor) Name() string { return "gaodun" }

// CanExtract checks if the extractor supports the given URL.
func (e *extractor) CanExtract(url string) bool {
	patterns := []string{
		`gaodun\.com.*course.*=\d+`,
		`gaodun\.com.*course/\d+`,
	}
	for _, pattern := range patterns {
		if matched, _ := regexp.MatchString(pattern, url); matched {
			return true
		}
	}
	return false
}

// extractCourseID parses the course ID from the URL.
func extractCourseID(url string) (string, error) {
	if matches := regexp.MustCompile(`(?:course_id|courseId)=(\d+)`).FindStringSubmatch(url); len(matches) >= 2 {
		return matches[1], nil
	}
	if matches := regexp.MustCompile(`/course/(\d+)`).FindStringSubmatch(url); len(matches) >= 2 {
		return matches[1], nil
	}
	return "", fmt.Errorf("course ID not found in URL")
}

// Extract fetches all media resources for a Gaodun course URL.
func (e *extractor) Extract(url string) ([]grab.Media, error) {
	e.api = NewApi(e.ctx.Client())
	courseID, err := extractCourseID(url)
	if err != nil {
		return nil, fmt.Errorf("failed to extract course ID: %w", err)
	}
	isGStudy, err := e.isGStudyCourse(courseID)
	if err != nil {
		return nil, fmt.Errorf("failed to determine course type: %w", err)
	}
	if isGStudy {
		return e.extractGStudyCourse(courseID)
	}
	return e.extractEpStudyCourse(courseID)
}

// isGStudyCourse returns true if the course is a G-Study course.
func (e *extractor) isGStudyCourse(courseID string) (bool, error) {
	gs, err := e.api.GStudy(courseID)
	if err != nil || len(gs) == 0 {
		return false, err
	}
	g := gs[0]
	return g.GSyllabus != nil && len(g.EpSyllabus) == 0, nil
}

// extractGStudyCourse fetches all media from a G-Study course.
func (e *extractor) extractGStudyCourse(courseID string) ([]grab.Media, error) {
	gradations, err := e.api.GStudy(courseID)
	if err != nil {
		return nil, err
	}
	return processConcurrently(gradations, func(grad Gradation) ([]grab.Media, error) {
		if grad.GSyllabus == nil {
			return nil, nil
		}
		syllabus, err := e.api.GStudySyllabus(courseID, grad.SyllabusID.String())
		if err != nil || syllabus == nil {
			e.ctx.Logger().Error("failed to get G-Study syllabus",
				"course_id", courseID, "gradation_name", grad.Name, "error", err)
			return nil, nil
		}
		return e.extractGStudySyllabus(courseID, grad.Name, *syllabus)
	})
}

// extractGStudySyllabus recursively collects all resources from a G-Study syllabus node.
func (e *extractor) extractGStudySyllabus(courseID, gradationName string, syllabus Syllabus) ([]grab.Media, error) {
	var allMedia []grab.Media
	if len(syllabus.Children) > 0 {
		childrenMedia, _ := e.processSyllabusConcurrently(syllabus.Children, func(child Syllabus) ([]grab.Media, error) {
			return e.extractGStudySyllabus(courseID, gradationName, child)
		})
		allMedia = append(allMedia, childrenMedia...)
	}
	allResources := append(append(append(
		syllabus.PreClassResource, syllabus.InClassMainResource...),
		syllabus.InClassAssistResource...), syllabus.AfterClassResource...)
	if len(allResources) > 0 {
		baseDir := e.buildResourcePath(courseID, gradationName, syllabus.Name)
		resourceMedia, err := e.extractResources(allResources, baseDir)
		if err != nil {
			e.ctx.Logger().Error("failed to extract resources",
				"course_id", courseID, "gradation_name", gradationName, "syllabus_name", syllabus.Name, "error", err)
		} else {
			allMedia = append(allMedia, resourceMedia...)
		}
	}
	return allMedia, nil
}

// extractEpStudyCourse fetches all media from an Ep-Study course.
func (e *extractor) extractEpStudyCourse(courseID string) ([]grab.Media, error) {
	gradations, err := e.api.EpStudy(courseID)
	if err != nil {
		return nil, fmt.Errorf("failed to get EP-Study gradations: %w", err)
	}
	return processConcurrently(gradations, func(grad Gradation) ([]grab.Media, error) {
		return e.processEpGradation(courseID, grad)
	})
}

// processEpGradation recursively collects all media from an Ep-Study gradation node.
func (e *extractor) processEpGradation(courseID string, grad Gradation) ([]grab.Media, error) {
	var allMedia []grab.Media
	if len(grad.Children) > 0 {
		childrenMedia, _ := processConcurrently(grad.Children, func(child Gradation) ([]grab.Media, error) {
			return e.processEpGradation(courseID, child)
		})
		allMedia = append(allMedia, childrenMedia...)
	}
	if grad.SyllabusID.String() != "0" && grad.SyllabusID.String() != "" {
		syllabusItems, err := e.api.EpStudySyllabus(courseID, grad.SyllabusID.String())
		if err != nil {
			return nil, fmt.Errorf("failed to get EP-Study syllabus for gradation %s (syllabus_id: %s): %w",
				grad.Name, grad.SyllabusID.String(), err)
		}
		media, err := e.extractEpStudySyllabus(courseID, grad.Name, syllabusItems)
		if err != nil {
			return nil, fmt.Errorf("failed to extract EP-Study syllabus items for gradation %s: %w", grad.Name, err)
		}
		allMedia = append(allMedia, media...)
	}
	return allMedia, nil
}

// extractEpStudySyllabus collects all resources from Ep-Study syllabus items.
func (e *extractor) extractEpStudySyllabus(courseID, gradationName string, syllabusItems []Syllabus) ([]grab.Media, error) {
	return e.processSyllabusConcurrently(syllabusItems, func(item Syllabus) ([]grab.Media, error) {
		return e.extractEpSyllabusItem(courseID, gradationName, item)
	})
}

// extractEpSyllabusItem recursively collects media from an Ep-Study syllabus item and its children.
func (e *extractor) extractEpSyllabusItem(courseID, gradationName string, item Syllabus) ([]grab.Media, error) {
	var allMedia []grab.Media
	if len(item.Children) > 0 {
		childrenMedia, _ := e.processSyllabusConcurrently(item.Children, func(child Syllabus) ([]grab.Media, error) {
			return e.extractEpSyllabusItem(courseID, gradationName, child)
		})
		allMedia = append(allMedia, childrenMedia...)
	}
	hasResource := (item.Is_Resource == 1 && item.Resource.ID != 0) || item.ResourceID != 0
	if hasResource {
		syllabusName := ""
		if item.Name != "" && item.Depth.String() != "0" {
			syllabusName = item.Name
		}
		itemDir := e.buildResourcePath(courseID, gradationName, syllabusName)
		var resourceToProcess Resource
		if item.Resource.ID != 0 {
			resourceToProcess = item.Resource
		} else if item.ResourceID != 0 {
			resourceToProcess = Resource{ID: item.ResourceID, Title: item.Name}
		}
		if resourceToProcess.ID != 0 {
			media, err := e.processResource(resourceToProcess, itemDir)
			if err != nil {
				return allMedia, fmt.Errorf("failed to process EP-Study resource (ID: %d, title: %s): %w",
					resourceToProcess.ID, resourceToProcess.Title, err)
			}
			if media == nil {
				e.ctx.Logger().Debug("skipping EP-Study resource with no media",
					"resource_id", resourceToProcess.ID, "title", resourceToProcess.Title)
				return allMedia, nil
			}
			allMedia = append(allMedia, *media)
		}
	}
	return allMedia, nil
}

// extractResources processes a list of resources and creates Media objects.
func (e *extractor) extractResources(resources []Resource, baseDir string) ([]grab.Media, error) {
	return e.processResourcesConcurrently(resources, func(res Resource) (*grab.Media, error) {
		return e.processResource(res, baseDir)
	})
}

// processResource creates a Media object from a Resource, handling different types.
func (e *extractor) processResource(resource Resource, baseDir string) (*grab.Media, error) {
	switch resource.Discriminator {
	case "live_new":
		if resource.LiveUrlPlayBackApp == "" {
			e.ctx.Logger().Debug("skipping live resource without playback URL",
				"resource_id", resource.ID, "title", resource.Title)
			return nil, nil
		}
		roomID, token, err := e.extractRoomIDAndToken(resource.LiveUrlPlayBackApp)
		if err != nil {
			e.ctx.Logger().Error("failed to extract room ID and token",
				"resource_id", resource.ID, "error", err)
			return nil, nil
		}
		code, err := e.api.GLiveCheck(roomID, token)
		if err != nil {
			e.ctx.Logger().Error("failed to check GLive",
				"room_id", roomID, "token", token, "error", err)
			return nil, nil
		}
		resource.VideoID = code
		return e.processVideoResource(resource, baseDir)
	case "video":
		return e.processVideoResource(resource, baseDir)
	case "lecture_note":
		return e.processNonVideoResource(resource, baseDir)
	}
	return nil, nil
}

// extractRoomIDAndToken parses room ID and token from a gaodunapp:// URL.
func (e *extractor) extractRoomIDAndToken(url string) (roomID, token string, err error) {
	roomIDPattern := regexp.MustCompile(`gaodunapp://gd/liveroom/v2/replays/detail\?recordId=([a-zA-Z0-9]+)&did=[a-zA-Z0-9]+&roomId=([a-zA-Z0-9-]+)&token=([a-zA-Z0-9]+)`)
	matches := roomIDPattern.FindStringSubmatch(url)
	if len(matches) >= 4 {
		return matches[2], matches[3], nil
	}
	return "", "", fmt.Errorf("room ID and token not found in URL")
}

// processVideoResource creates a Media object for a video resource.
func (e *extractor) processVideoResource(resource Resource, baseDir string) (*grab.Media, error) {
	sourceID := resource.VideoID
	videoRes, err := e.api.VideoResource(sourceID, "SD", 0)
	if err != nil {
		return nil, err
	}
	streams := make([]grab.Stream, 0, len(videoRes.List))
	for quality, qualityInfo := range videoRes.List {
		if qualityInfo.Available != 1 || qualityInfo.Path == "" {
			continue
		}
		id := resource.VideoID + "_" + quality
		stream := grab.Stream{
			ID:       id,
			Title:    resource.Title,
			Type:     grab.StreamTypeM3u8,
			Format:   "mp4",
			URL:      qualityInfo.Path,
			Quality:  qualityInfo.Resolution.Resolution,
			Size:     int64(qualityInfo.FileSize * 1024),
			Duration: time.Duration(resource.Duration) * time.Second,
			SaveAs:   filepath.Join(baseDir, fmt.Sprintf("%s_%s.mp4", utils.SanitizeFilename(resource.Title), quality)),
			Header:   resourceHeaders(qualityInfo.Path),
		}
		streams = append(streams, stream)
	}
	if len(streams) == 0 {
		return nil, fmt.Errorf("no available video streams")
	}
	return &grab.Media{
		Title:   resource.Title,
		Streams: streams,
	}, nil
}

// processNonVideoResource creates a Media object for a non-video resource (e.g., PDF).
func (e *extractor) processNonVideoResource(res Resource, baseDir string) (*grab.Media, error) {
	if res.Path == "" {
		return nil, nil
	}
	ext := res.Extension
	if ext == "" {
		switch res.Mime {
		case "application/pdf":
			ext = "pdf"
		default:
			ext = "file"
		}
	}
	filename := utils.SanitizeFilename(res.Title)
	if filename == "" {
		filename = fmt.Sprintf("document_%d", res.ID)
	}
	size, err := res.Filesize.Int64()
	if err != nil {
		size = 0
	}
	if strings.HasPrefix(res.Path, "//") {
		res.Path = "https:" + res.Path
	}
	streams := []grab.Stream{{
		ID:      strconv.Itoa(res.ID),
		Title:   res.Title,
		Type:    grab.StreamTypeDocument,
		Format:  ext,
		URL:     res.Path,
		Quality: "best",
		Size:    size,
		SaveAs:  filepath.Join(baseDir, fmt.Sprintf("%s.%s", filename, ext)),
		Header:  resourceHeaders(res.Path),
	}}
	return &grab.Media{
		Title:   res.Title,
		Streams: streams,
	}, nil
}

// resourceHeaders returns HTTP headers for resource requests.
func resourceHeaders(url string) http.Header {
	headers := make(http.Header)
	headers.Set("Referer", "https://glive2.gaodun.com")
	headers.Set("User-Agent", "GdClient/10.0.82 Android/14 H2OS/110_14.0.0.630(cn01) Player/2.3.0 EXO/2.12.0")
	headers.Set("Host", utils.ExtractDomain(url))
	headers.Set("Connection", "Keep-Alive")
	return headers
}

// buildResourcePath generates a consistent directory path for resources.
func (e *extractor) buildResourcePath(courseID, gradationName, syllabusName string) string {
	parts := []string{courseID}
	if gradationName != "" {
		parts = append(parts, utils.SanitizeFilename(gradationName))
	}
	if syllabusName != "" {
		parts = append(parts, utils.SanitizeFilename(syllabusName))
	}
	return filepath.Join(parts...)
}

// processConcurrently runs fn for each item concurrently and aggregates results.
// Only the first error is returned. Panics are recovered to avoid goroutine leaks.
func processConcurrently[T any](items []T, fn func(T) ([]grab.Media, error)) ([]grab.Media, error) {
	var wg sync.WaitGroup
	var mu sync.Mutex
	var once sync.Once
	var allMedia []grab.Media
	var firstErr error
	for _, item := range items {
		wg.Add(1)
		go func(item T) {
			defer func() {
				if r := recover(); r != nil {
					once.Do(func() { firstErr = fmt.Errorf("panic: %v", r) })
				}
				wg.Done()
			}()
			media, err := fn(item)
			if err != nil {
				once.Do(func() { firstErr = err })
			}
			if len(media) > 0 {
				mu.Lock()
				allMedia = append(allMedia, media...)
				mu.Unlock()
			}
		}(item)
	}
	wg.Wait()
	return allMedia, firstErr
}

// processSyllabusConcurrently runs fn for each syllabus concurrently and aggregates results.
func (e *extractor) processSyllabusConcurrently(items []Syllabus, fn func(Syllabus) ([]grab.Media, error)) ([]grab.Media, error) {
	return processConcurrently(items, fn)
}

// processResourcesConcurrently runs fn for each resource concurrently and aggregates results.
func (e *extractor) processResourcesConcurrently(items []Resource, fn func(Resource) (*grab.Media, error)) ([]grab.Media, error) {
	var wg sync.WaitGroup
	var mu sync.Mutex
	var once sync.Once
	var allMedia []grab.Media
	var firstErr error
	for _, item := range items {
		wg.Add(1)
		go func(item Resource) {
			defer func() {
				if r := recover(); r != nil {
					once.Do(func() { firstErr = fmt.Errorf("panic: %v", r) })
				}
				wg.Done()
			}()
			media, err := fn(item)
			if err != nil {
				once.Do(func() { firstErr = err })
			}
			if media != nil {
				mu.Lock()
				allMedia = append(allMedia, *media)
				mu.Unlock()
			}
		}(item)
	}
	wg.Wait()
	return allMedia, firstErr
}
