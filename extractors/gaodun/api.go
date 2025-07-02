package gaodun

import (
	"crypto/rand"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-resty/resty/v2"
	"github.com/gregjones/httpcache"
	"github.com/gregjones/httpcache/diskcache"
)

const (
	// API endpoints
	endpoint = "https://apigateway.gaodun.com"

	// Headers required for API requests
	userAgent  = "GdClient/10.0.81 Android/14 H2OS/110_14.0.0.630(cn01) GdNetwork/1.0.5"
	apiVersion = "264"
)

var ErrAbortWithResponse = errors.New("abort with response")

// Api defines the interface for Gaodun API operations
type Api interface {
	// GStudy retrieves syllabus information for g-study courses
	// GET https://apigateway.gaodun.com/g-study/api/v1/front/course/{courseID}/gradation/syllabus
	GStudy(courseID string) ([]Gradation, error)

	// GStudySyllabus retrieves detailed syllabus for glive courses
	// GET https://apigateway.gaodun.com/g-study/api/v1/front/course/{courseID}/syllabus/glive/{syllabusID}
	GStudySyllabus(courseID, syllabusID string) (*Syllabus, error)

	// GLiveCheck get real video ID for glive vod
	GLiveCheck(roomId, token string) (string, error)

	// EpStudy retrieves gradation for ep-study courses
	// GET https://apigateway.gaodun.com/ep-study/front/course/{courseID}/gradation
	EpStudy(courseID string) ([]Gradation, error)

	// EpStudySyllabus retrieves detailed syllabus for ep-study courses
	// GET https://apigateway.gaodun.com/ep-study/front/course/{courseID}/syllabus/{syllabusID}
	EpStudySyllabus(courseID, syllabusID string) ([]Syllabus, error)

	// VideoResource retrieves video stream information
	// GET https://apigateway.gaodun.com/glive2-vod/api/v1/live/resource?code={code}&res={res}&channel={channel}
	VideoResource(code, res string, channel int) (*VideoResource, error)

	// headers returns the headers used for API requests
	Headers() http.Header
}

// NewApi creates a new API client with proper authentication headers
func NewApi(client *resty.Client) Api {
	if client == nil {
		client = resty.New()
	}

	client.SetBaseURL(endpoint)

	if client.Header.Get("Authentication") == "" {
		token := os.Getenv("GAODUN_AUTH_TOKEN")
		client.SetHeader("Authentication", token)
	}

	xRequestedExtend := fmt.Sprintf(
		`{"apiConfigVersion":"%s","appStore":"%s","appVersion":"%s","phoneBrand":"%s","appScheme":"%s","deviceId":"%s","appChannel":"%s","appChannelName":"%s"}`,
		apiVersion, "oppo", "264", "oneplus", "gaodunapp", generateDeviceID(), "oppo", "android",
	)
	client.SetHeader("User-Agent", userAgent)
	client.SetHeader("ApiVersion", apiVersion)
	client.SetHeader("X-Requested-Extend", xRequestedExtend)
	client.SetHeader("Host", "apigateway.gaodun.com")
	client.SetHeader("Connection", "Keep-Alive")
	client.SetHeader("Accept-Encoding", "gzip")

	cachePath := filepath.Join(os.TempDir(), "gaodun_cache")
	cache := diskcache.New(cachePath)
	transport := httpcache.NewTransport(cache)
	client.SetTransport(transport)

	client.OnAfterResponse(func(c *resty.Client, r *resty.Response) error {
		if r.StatusCode() != http.StatusOK {
			return fmt.Errorf("API request failed with status %d: %s", r.StatusCode(), r.String())
		}

		if strings.Contains(r.String(), "Unable to verify token") {
			return fmt.Errorf("authentication failed: %w", ErrAbortWithResponse)
		}

		return nil
	})

	return &api{
		client: client,
	}
}

// api handles all API interactions with Gaodun services
type api struct {
	client *resty.Client
}

// GStudy retrieves syllabus information for g-study courses
func (c *api) GStudy(courseID string) ([]Gradation, error) {
	url := fmt.Sprintf("/g-study/api/v1/front/course/%s/gradation/syllabus", courseID)

	var resp apiResponse[[]Gradation]

	_, err := c.client.R().
		SetResult(&resp).
		Get(url)

	if err != nil {
		return nil, fmt.Errorf("failed to get g-study syllabus: %w", err)
	}

	return resp.Result, nil
}

// GStudySyllabus retrieves detailed syllabus for glive courses
func (c *api) GStudySyllabus(courseID, syllabusID string) (*Syllabus, error) {
	url := fmt.Sprintf("/g-study/api/v1/front/course/%s/syllabus/glive/%s", courseID, syllabusID)

	var resp apiResponse[Syllabus]

	_, err := c.client.R().
		SetResult(&resp).
		Get(url)

	if err != nil {
		return nil, fmt.Errorf("failed to get g-study syllabus: %w", err)
	}

	return &resp.Result, nil
}

// EpStudy retrieves gradation for ep-study courses
func (c *api) EpStudy(courseID string) ([]Gradation, error) {
	url := fmt.Sprintf("/ep-study/front/course/%s/gradation", courseID)

	var resp apiResponse[[]Gradation]
	_, err := c.client.R().
		SetResult(&resp).
		Get(url)

	if err != nil {
		return nil, fmt.Errorf("failed to get ep-study gradation: %w", err)
	}

	return resp.Result, nil
}

// EpStudySyllabus retrieves detailed syllabus for ep-study courses
func (c *api) EpStudySyllabus(courseID, syllabusID string) ([]Syllabus, error) {
	url := fmt.Sprintf("/ep-study/front/course/%s/syllabus/%s?show_own_teacher=true", courseID, syllabusID)

	var res apiResponse[struct {
		Item []Syllabus `json:"items"`
	}]

	_, err := c.client.R().
		SetResult(&res).
		Get(url)

	if err != nil {
		return nil, fmt.Errorf("failed to get ep-study syllabus: %w", err)
	}

	return res.Result.Item, nil
}

// VideoResource retrieves video stream information
func (c *api) VideoResource(code, quality string, channel int) (*VideoResource, error) {
	url := fmt.Sprintf("/glive2-vod/api/v1/live/resource?code=%s&res=%s&channel=%d", code, quality, channel)

	var res apiResponse[VideoResource]

	_, err := c.client.R().
		SetHeader("isLiveVodAuthenticate", "true").
		SetResult(&res).
		Get(url)
	if err != nil {
		return nil, fmt.Errorf("failed to get video resource: %w", err)
	}

	return &res.Result, nil
}

func (c *api) GLiveCheck(roomId, token string) (string, error) {
	url := fmt.Sprintf("/glive2-vod/api/v1/vod/check?roomId=%s&token=%s", roomId, token)

	type result struct {
		Code string `json:"code"`
	}

	var res apiResponse[result]

	_, err := c.client.R().
		SetHeader("isLiveVodAuthenticate", "true").
		SetResult(&res).
		Get(url)

	if err != nil {
		return "", fmt.Errorf("failed to check glive vod: %w", err)
	}

	return res.Result.Code, nil
}

// Headers returns the headers used for API requests
func (c *api) Headers() http.Header {
	return c.client.Header.Clone()
}

// generateDeviceID generates a random device ID for API requests
func generateDeviceID() string {
	b := make([]byte, 33)
	_, err := rand.Read(b)
	if err != nil {
		return "24ca6c8e5eed9334b822c28eda895e70a" // Default value if random generation fails
	}
	b[0] = '2' // Ensure it starts with '2' to match the expected format
	return fmt.Sprintf("%x", b)
}
