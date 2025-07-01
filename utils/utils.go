package utils

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// FormatBytes converts bytes to human readable string
func FormatBytes(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}

// FormatDuration formats a duration in seconds to a human-readable string
func FormatDuration(seconds time.Duration) string {
	if seconds < 0 {
		return "N/A"
	}
	hours := int(seconds.Hours())
	minutes := int(seconds.Minutes()) % 60
	secs := int(seconds.Seconds()) % 60

	if hours > 0 {
		return fmt.Sprintf("%02d:%02d:%02d", hours, minutes, secs)
	}
	return fmt.Sprintf("%02d:%02d", minutes, secs)
}

// SanitizeFilename removes invalid characters from filename
func SanitizeFilename(filename string) string {
	// Replace invalid characters with underscore
	invalid := regexp.MustCompile(`[<>:"/\\|?*]`)
	filename = invalid.ReplaceAllString(filename, "_")

	// Remove leading/trailing spaces and dots
	filename = strings.Trim(filename, " .")

	// Limit length to 255 characters (common filesystem limit)
	if len(filename) > 255 {
		filename = filename[:255]
	}

	return filename
}

// FileExtension returns the file extension (including the dot) from a filename or URL.
// If the filename starts with a dot and has no other dot, it returns an empty string (e.g., ".hiddenfile" -> "").
// If there is no extension, it returns an empty string.
func FileExtension(filename string) string {
	if i := strings.LastIndex(filename, "."); i > 0 && i < len(filename)-1 {
		return filename[i:]
	}
	return ""
}

// IsValidURL checks if the string is a valid URL
func IsValidURL(url string) bool {
	return strings.HasPrefix(url, "http://") || strings.HasPrefix(url, "https://")
}

// ExtractDomain extracts domain from URL
func ExtractDomain(url string) string {
	// Remove protocol
	url = strings.TrimPrefix(url, "http://")
	url = strings.TrimPrefix(url, "https://")

	// Extract domain part
	parts := strings.Split(url, "/")
	if len(parts) == 0 {
		return ""
	}

	domain := parts[0]

	// Remove port if exists
	if idx := strings.Index(domain, ":"); idx != -1 {
		domain = domain[:idx]
	}

	return domain
}
