package utils

import "net/http"

// MergeHeader merges two http.Header objects into one.
func MergeHeader(original, additional http.Header) http.Header {
	merged := original.Clone()
	for k, v := range additional {
		merged[k] = v
	}
	return merged
}
