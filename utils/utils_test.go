package utils

import (
	"strings"
	"testing"
)

// TestFormatBytes verifies FormatBytes returns human-readable strings for various byte sizes.
func TestFormatBytes(t *testing.T) {
	tests := []struct {
		input int64
		want  string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1023, "1023 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{1048576, "1.0 MB"},
		{1073741824, "1.0 GB"},
		{1099511627776, "1.0 TB"},
	}
	for _, tt := range tests {
		got := FormatBytes(tt.input)
		if got != tt.want {
			t.Errorf("FormatBytes(%d) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// TestFormatDuration verifies FormatDuration returns correct time strings.
func TestFormatDuration(t *testing.T) {
	tests := []struct {
		input int
		want  string
	}{
		{-1, "0:00"},
		{0, "0:00"},
		{5, "0:05"},
		{65, "1:05"},
		{3600, "1:00:00"},
		{3661, "1:01:01"},
	}
	for _, tt := range tests {
		got := FormatDuration(tt.input)
		if got != tt.want {
			t.Errorf("FormatDuration(%d) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// TestSanitizeFilename verifies SanitizeFilename removes invalid characters and trims.
func TestSanitizeFilename(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"abc.txt", "abc.txt"},
		{"a<b>c:d|e?f*g.txt", "a_b_c_d_e_f_g.txt"},
		{"  foo.txt ", "foo.txt"},
		{"...bar...", "bar"},
		{strings.Repeat("a", 300), strings.Repeat("a", 255)},
	}
	for _, tt := range tests {
		got := SanitizeFilename(tt.input)
		if got != tt.want {
			t.Errorf("SanitizeFilename(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// TestFileExtension verifies GetFileExtension extracts the extension correctly.
func TestFileExtension(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"file", ""},
		{"file.txt", ".txt"},
		{"archive.tar.gz", ".gz"},
		{".hiddenfile", ""},
	}
	for _, tt := range tests {
		got := FileExtension(tt.input)
		if got != tt.want {
			t.Errorf("GetFileExtension(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// TestIsValidURL verifies IsValidURL checks for http/https schemes.
func TestIsValidURL(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"http://example.com", true},
		{"https://example.com", true},
		{"ftp://example.com", false},
		{"example.com", false},
	}
	for _, tt := range tests {
		got := IsValidURL(tt.input)
		if got != tt.want {
			t.Errorf("IsValidURL(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

// TestExtractDomain verifies ExtractDomain extracts the domain part from URLs.
func TestExtractDomain(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"http://example.com/path", "example.com"},
		{"https://example.com:8080/path", "example.com"},
		{"example.com/path", "example.com"},
		{"", ""},
	}
	for _, tt := range tests {
		got := ExtractDomain(tt.input)
		if got != tt.want {
			t.Errorf("ExtractDomain(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
