package grab

import "errors"

var (
	ErrNoExtractorFound = errors.New("no extractor found for the given URL")
	ErrInvalidURL       = errors.New("invalid URL provided")
	ErrFFmpegNotFound   = errors.New("ffmpeg executable not found in PATH")
)
