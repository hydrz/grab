package gaodun

import (
	"encoding/json"
)

// apiResponse represents the common response structure for all Gaodun API endpoints
type apiResponse[T any] struct {
	Status  int    `json:"status"`
	Message string `json:"message"`
	Result  T      `json:"result"`
}

// Gradation represents course stage items from Gradation endpoint
type Gradation struct {
	ID          json.Number `json:"id"`
	Name        string      `json:"name"`
	Description string      `json:"description,omitempty"`
	SyllabusID  json.Number `json:"syllabus_id,omitempty"`
	CourseID    json.Number `json:"course_id,omitempty"`
	Children    []Gradation `json:"children,omitempty"`
	GSyllabus   *Syllabus   `json:"gliveSyllabus,omitempty"`
	EpSyllabus  []Syllabus  `json:"epSyllabus,omitempty"`
}

func cond[T any](cond bool, a, b T) T {
	if cond {
		return a
	}
	return b
}

func (g *Gradation) UnmarshalJSON(data []byte) error {
	type Alias Gradation
	aux := &struct {
		SyllabusID  json.Number `json:"syllabusId,omitempty"`
		SyllabusID2 json.Number `json:"syllabus_id,omitempty"`
		CourseID    json.Number `json:"courseId,omitempty"`
		CourseID2   json.Number `json:"course_id,omitempty"`
		*Alias
	}{
		Alias: (*Alias)(g),
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	g.SyllabusID = cond(aux.SyllabusID != "", aux.SyllabusID, aux.SyllabusID2)
	g.CourseID = cond(aux.CourseID != "", aux.CourseID, aux.CourseID2)
	return nil
}

// Syllabus represents a Syllabus item (chapter/lesson)
type Syllabus struct {
	ID                    int         `json:"id"`
	ItemID                int         `json:"itemId,omitempty"`
	Name                  string      `json:"name"`
	Type                  json.Number `json:"type,omitempty"`
	Depth                 json.Number `json:"depth,omitempty"`
	IsResource            int         `json:"isResource,omitempty"`
	Resource              Resource    `json:"resource,omitempty"`
	Children              []Syllabus  `json:"children,omitempty"`
	PreClassResource      []Resource  `json:"preClassResource,omitempty"`
	InClassMainResource   []Resource  `json:"inClassMainResource,omitempty"`
	InClassAssistResource []Resource  `json:"inClassAssistResource,omitempty"`
	AfterClassResource    []Resource  `json:"afterClassResource,omitempty"`

	// Additional fields for ep-study endpoints (with different JSON field names)
	Item_ID        int    `json:"item_id,omitempty"`
	Total_Resource int    `json:"total_resource,omitempty"`
	Parent_ID      string `json:"parent_id,omitempty"`
	ResourceID     int    `json:"resource_id,omitempty"`
	Is_Resource    int    `json:"is_resource,omitempty"`
}

// Resource represents a Resource item (video, document, etc.)
type Resource struct {
	ID                 int         `json:"id"`
	Title              string      `json:"title"`
	Duration           int         `json:"duration,omitempty"`
	Category           int         `json:"category,omitempty"`
	Discriminator      string      `json:"discriminator,omitempty"`
	Description        string      `json:"description,omitempty"`
	URI                string      `json:"uri,omitempty"`
	Extension          string      `json:"extension,omitempty"`
	Mime               string      `json:"mime,omitempty"`
	Path               string      `json:"path,omitempty"`
	VideoID            string      `json:"video_id,omitempty"`
	LiveStatus         json.Number `json:"liveStatus,omitempty"`
	Filesize           json.Number `json:"filesize,omitempty"`
	FileSizeHuman      string      `json:"fileSize,omitempty"`
	LiveUrlPlayBackApp string      `json:"liveUrlPlayBackApp,omitempty"`
	RelatedResource    []Resource  `json:"related_resource,omitempty"`
}

func (r *Resource) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		*r = Resource{}
		return nil
	}

	type Alias Resource
	aux := &struct {
		Size json.Number `json:"size,omitempty"`
		*Alias
	}{
		Alias: (*Alias)(r),
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}

	if aux.Size != "" {
		r.Filesize = aux.Size
	}

	return nil
}

// VideoQuality represents video quality information
type VideoQuality struct {
	Available   int    `json:"available"`
	FileSize    int    `json:"file_size"`
	IsWatermark int    `json:"is_watermark"`
	Path        string `json:"path"`
	Resolution  struct {
		Name       string `json:"name"`
		NameSimple string `json:"name_simple"`
		Resolution string `json:"resolution"`
	} `json:"resolution"`
	TranscodeID string `json:"transcode_id"`
}

// VideoResource represents video resource response from live/resource endpoint
type VideoResource struct {
	DefaultType string                  `json:"defaultType"`
	Duration    int                     `json:"duration"`
	Encrypt     int                     `json:"encrypt"`
	List        map[string]VideoQuality `json:"list"`
	Title       string                  `json:"title"`
}

// EpSyllabusResponse represents the response structure for ep-study syllabus endpoint
type EpSyllabusResponse struct {
	Items      []Syllabus `json:"items"`
	SyllabusID int        `json:"syllabus_id"`
}
