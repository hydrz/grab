package grab

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// convertFormat uses ffmpeg to convert input file to the specified format.
// Returns the output file path or error.
func convertFormat(inputPath, outputFormat string) (string, error) {
	ffmpegBin := "ffmpeg"
	if runtime.GOOS == "windows" {
		ffmpegBin = "ffmpeg.exe"
	}
	ffmpegPath, err := exec.LookPath(ffmpegBin)
	if err != nil {
		return "", ErrFFmpegNotFound
	}

	ext := "." + strings.ToLower(outputFormat)
	outputPath := strings.TrimSuffix(inputPath, filepath.Ext(inputPath)) + ext

	cmd := exec.Command(ffmpegPath, "-y", "-i", inputPath, outputPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("ffmpeg failed: %v, output: %s", err, string(output))
	}
	return outputPath, nil
}
