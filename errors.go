package grab

import "errors"

var (
	ErrNoExtractorFound = errors.New("grab: no extractor found for the given URL")
)
