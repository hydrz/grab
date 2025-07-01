package grab

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// checkFFmpeg checks if ffmpeg is available in system PATH
func checkFFmpeg() error {
	_, err := exec.LookPath("ffmpeg")
	if err != nil {
		return ErrFFmpegNotFound
	}
	return nil
}

// concatenateWithFFmpeg concatenates video segments using ffmpeg
func concatenateWithFFmpeg(segmentPaths []string, outputPath string) error {
	if err := checkFFmpeg(); err != nil {
		return err
	}

	if len(segmentPaths) == 0 {
		return fmt.Errorf("no segments to concatenate")
	}

	// Create a temporary file list for ffmpeg concat
	listFile := filepath.Join(filepath.Dir(outputPath), "concat_list.txt")
	defer os.Remove(listFile)

	// Write file list
	var content strings.Builder
	for _, segmentPath := range segmentPaths {
		// Make path absolute for ffmpeg
		absPath, err := filepath.Abs(segmentPath)
		if err != nil {
			absPath = segmentPath
		}
		content.WriteString(fmt.Sprintf("file '%s'\n", absPath))
	}

	if err := os.WriteFile(listFile, []byte(content.String()), 0644); err != nil {
		return fmt.Errorf("failed to create concat list: %w", err)
	}

	// Run ffmpeg concat
	args := []string{
		"-f", "concat",
		"-safe", "0",
		"-i", listFile,
		"-c", "copy",
		"-y", // Overwrite output file
		outputPath,
	}

	cmd := exec.Command("ffmpeg", args...)
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ffmpeg failed: %w", err)
	}

	return nil
}
