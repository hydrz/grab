package grab

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/hydrz/grab/utils"
)

// Downloader manages high-level download logic, supporting HTTP range requests, resumable and multi-threaded downloads.
type Downloader struct {
	ctx    *Context
	mu     sync.RWMutex
	cancel context.CancelFunc
}

// NewDownloader creates a new Downloader instance with the provided context.
// ProgressCallback can be set after creation for progress reporting.
func NewDownloader(ctx *Context) *Downloader {
	return &Downloader{ctx: ctx}
}

// Download downloads all streams from the extracted media for the given URL.
// If ExtractOnly is set, only prints media info without downloading.
func (d *Downloader) Download(medias []Media) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	// Create cancellable context for graceful shutdown
	ctx, cancel := context.WithCancel(d.ctx.Context())
	d.cancel = cancel
	defer cancel()

	for _, media := range medias {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		d.ctx.logger.Debug("Downloading media", "title", media.Title)
		if err := d.downloadMedia(ctx, media); err != nil {
			d.ctx.logger.Error("Failed to download media", "title", media.Title, "error", err)
			if d.ctx.option.IgnoreErrors {
				continue
			}
			return fmt.Errorf("failed to download media %s: %w", media.Title, err)
		}
	}

	return nil
}

// Stop gracefully cancels all ongoing downloads
func (d *Downloader) Stop() {
	if d.cancel != nil {
		d.cancel()
	}
}

// downloadMedia downloads all streams for a given media, applying filters and error handling.
func (d *Downloader) downloadMedia(ctx context.Context, media Media) error {
	if len(media.Streams) == 0 {
		return fmt.Errorf("no streams available for media %s", media.Title)
	}

	filters := d.ctx.option.FiltersForStreams(media.Streams)
	for _, stream := range media.Streams {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if d.shouldSkipStream(stream, filters) {
			continue
		}

		d.ctx.logger.Debug("Downloading stream", "id", stream.ID, "type", stream.Type, "quality", stream.Quality)
		if err := d.downloadStream(ctx, stream); err != nil {
			d.ctx.logger.Error("Failed to download stream", "id", stream.ID, "error", err)
			if d.ctx.option.IgnoreErrors {
				continue
			}
			return fmt.Errorf("failed to download stream %s: %w", stream.ID, err)
		}
	}

	return nil
}

// shouldSkipStream returns true if the stream should be skipped according to filters.
func (d *Downloader) shouldSkipStream(stream Stream, filters []Filter) bool {
	if len(filters) == 0 {
		return false
	}
	for _, filter := range filters {
		if !filter.Filter(stream) {
			d.ctx.logger.Debug("Skipping stream", "id", stream.ID, "reason", filter)
			return true
		}
	}
	return false
}

// downloadStream dispatches the download logic based on stream type.
func (d *Downloader) downloadStream(ctx context.Context, stream Stream) error {
	outputDir := d.getOutputDir(stream)
	filename := d.getOutputFilename(stream)
	outputPath := filepath.Join(outputDir, filename)

	if d.ctx.option.SkipExisting {
		if fi, err := os.Stat(outputPath); err == nil && fi.Size() > 0 {
			d.ctx.logger.Debug("File already exists, skipping", "path", outputPath)
			return nil
		}
	}

	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("failed to create output directory %s: %w", outputDir, err)
	}

	var err error
	if stream.Type == StreamTypeM3u8 {
		err = d.downloadM3U8Stream(ctx, stream, outputPath)
	} else if d.ctx.option.Threads > 1 {
		if supportsRange, size := d.checkRangeSupport(ctx, stream); supportsRange && size > d.ctx.option.ChunkSize {
			err = d.downloadWithChunks(ctx, stream, outputPath, size)
		} else {
			err = d.downloadSingleThread(ctx, stream, outputPath)
		}
	} else {
		err = d.downloadSingleThread(ctx, stream, outputPath)
	}
	if err != nil {
		return err
	}

	// Output format conversion
	if d.ctx.option.Format != "" && d.ctx.option.Format != stream.Format {
		d.ctx.logger.Info("Converting format", "from", stream.Format, "to", d.ctx.option.Format)
		convertedPath, convErr := ConvertFormat(outputPath, d.ctx.option.Format)
		if convErr != nil {
			return fmt.Errorf("format conversion failed: %w", convErr)
		}
		d.ctx.logger.Info("Format conversion completed", "output", convertedPath)
	}

	return nil
}

// checkRangeSupport checks if the server supports HTTP range requests
func (d *Downloader) checkRangeSupport(ctx context.Context, stream Stream) (bool, int64) {
	req := d.ctx.client.R().
		SetContext(ctx).
		SetHeader("Range", "bytes=0-0")
	req.Header = stream.Header.Clone()

	resp, err := req.Head(stream.URL)
	if err != nil {
		return false, 0
	}

	acceptsRanges := resp.Header().Get("Accept-Ranges") == "bytes"
	contentRange := resp.Header().Get("Content-Range")

	if !acceptsRanges && contentRange == "" {
		return false, 0
	}

	// Get total size from Content-Length or Content-Range
	var totalSize int64
	if contentLength := resp.Header().Get("Content-Length"); contentLength != "" {
		if size, err := strconv.ParseInt(contentLength, 10, 64); err == nil {
			totalSize = size
		}
	}

	// Parse Content-Range for total size if available
	if contentRange != "" {
		parts := strings.Split(contentRange, "/")
		if len(parts) == 2 && parts[1] != "*" {
			if size, err := strconv.ParseInt(parts[1], 10, 64); err == nil {
				totalSize = size
			}
		}
	}

	return acceptsRanges || contentRange != "", totalSize
}

// downloadWithChunks performs multi-threaded download with resume capability
func (d *Downloader) downloadWithChunks(ctx context.Context, stream Stream, outputPath string, totalSize int64) error {
	downloader := &ChunkDownloader{
		client:     d.ctx.client,
		url:        stream.URL,
		headers:    stream.Header,
		totalSize:  totalSize,
		chunkSize:  d.ctx.option.ChunkSize,
		threads:    d.ctx.option.Threads,
		outputPath: outputPath,
		ctx:        ctx,
	}

	// Setup progress tracking
	progress := NewProgress(totalSize, fmt.Sprintf("Downloading %s", stream.Title))
	if callback := d.ctx.GetProgressCallback(); callback != nil {
		progress.SetCallback(callback)
	}
	downloader.progress = progress

	return downloader.Download()
}

// downloadSingleThread performs single-threaded download with resume capability
func (d *Downloader) downloadSingleThread(ctx context.Context, stream Stream, outputPath string) error {
	var resumeOffset int64

	// Check for existing partial file
	if fi, err := os.Stat(outputPath); err == nil {
		resumeOffset = fi.Size()
		d.ctx.logger.Debug("Resuming download", "path", outputPath, "offset", resumeOffset)
	}

	req := d.ctx.client.R().
		SetContext(ctx).
		SetDoNotParseResponse(true)
	req.Header = stream.Header.Clone()

	// Set range header for resume if needed
	if resumeOffset > 0 {
		req.SetHeader("Range", fmt.Sprintf("bytes=%d-", resumeOffset))
	}

	resp, err := req.Get(stream.URL)
	if err != nil {
		return fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.RawBody().Close()

	// Check response status
	expectedStatus := http.StatusOK
	if resumeOffset > 0 {
		expectedStatus = http.StatusPartialContent
	}

	if resp.StatusCode() != expectedStatus && resp.StatusCode() != http.StatusOK {
		return fmt.Errorf("HTTP error: %s", resp.Status())
	}

	// Open output file for writing (append mode if resuming)
	var file *os.File
	if resumeOffset > 0 {
		file, err = os.OpenFile(outputPath, os.O_WRONLY|os.O_APPEND, 0644)
	} else {
		file, err = os.Create(outputPath)
	}
	if err != nil {
		return fmt.Errorf("failed to open output file: %w", err)
	}
	defer file.Close()

	// Get total size for progress tracking
	var totalSize int64 = stream.Size
	if contentLength := resp.Header().Get("Content-Length"); contentLength != "" {
		if size, err := strconv.ParseInt(contentLength, 10, 64); err == nil {
			totalSize = size + resumeOffset
		}
	}

	// Progress tracking
	progress := NewProgress(totalSize, fmt.Sprintf("Downloading %s", stream.Title))
	if callback := d.ctx.GetProgressCallback(); callback != nil {
		progress.SetCallback(callback)
	}

	// Set initial progress if resuming
	if resumeOffset > 0 {
		progress.Add(resumeOffset)
	}

	// Wrap response body with progress tracking
	reader := progress.NewReader(resp.RawBody())
	defer reader.Close()

	// Copy with context checking
	_, err = d.copyWithContext(ctx, file, reader)
	if err != nil {
		return fmt.Errorf("failed to write to output file: %w", err)
	}

	return nil
}

// downloadM3U8Stream handles M3U8 streams
func (d *Downloader) downloadM3U8Stream(ctx context.Context, stream Stream, outputPath string) error {
	data, err := d.processM3U8(stream)
	if err != nil {
		return fmt.Errorf("failed to process M3U8 stream: %w", err)
	}
	defer data.Close()

	// Create output file
	file, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("failed to create output file: %w", err)
	}
	defer file.Close()

	// Progress tracking
	progress := NewProgress(stream.Size, fmt.Sprintf("Downloading %s", stream.Title))
	if callback := d.ctx.GetProgressCallback(); callback != nil {
		progress.SetCallback(callback)
	}

	reader := progress.NewReader(data)
	defer reader.Close()

	_, err = d.copyWithContext(ctx, file, reader)
	if err != nil {
		return fmt.Errorf("failed to write to output file: %w", err)
	}

	return nil
}

// copyWithContext copies data with context cancellation support
func (d *Downloader) copyWithContext(ctx context.Context, dst io.Writer, src io.Reader) (written int64, err error) {
	buf := make([]byte, 32*1024) // 32KB buffer
	for {
		select {
		case <-ctx.Done():
			return written, ctx.Err()
		default:
		}

		nr, er := src.Read(buf)
		if nr > 0 {
			nw, ew := dst.Write(buf[0:nr])
			if nw < 0 || nr < nw {
				nw = 0
				if ew == nil {
					ew = fmt.Errorf("invalid write result")
				}
			}
			written += int64(nw)
			if ew != nil {
				err = ew
				break
			}
			if nr != nw {
				err = io.ErrShortWrite
				break
			}
		}
		if er != nil {
			if er != io.EOF {
				err = er
			}
			break
		}
	}
	return written, err
}

// getOutputDir returns the output directory for a stream, considering SaveAs and OutputPath.
func (d *Downloader) getOutputDir(stream Stream) string {
	if stream.SaveAs != "" {
		dir := filepath.Dir(stream.SaveAs)
		if filepath.IsAbs(dir) {
			return dir
		}
		return filepath.Join(d.ctx.option.OutputPath, dir)
	}
	return d.ctx.option.OutputPath
}

// getOutputFilename returns the output filename for a stream, considering OutputName and SaveAs.
func (d *Downloader) getOutputFilename(stream Stream) string {
	if d.ctx.option.OutputName != "" {
		ext := utils.FileExtension(d.ctx.option.OutputName)
		if ext == "" {
			ext = "." + stream.Format
		}
		name := d.ctx.option.OutputName
		if !strings.HasSuffix(name, ext) {
			name += ext
		}
		return utils.SanitizeFilename(name)
	}
	if stream.SaveAs != "" {
		return utils.SanitizeFilename(filepath.Base(stream.SaveAs))
	}
	title := stream.Title
	if title == "" {
		title = "download"
	}
	ext := stream.Format
	if ext == "" {
		ext = "mp4"
	}
	return fmt.Sprintf("%s.%s", utils.SanitizeFilename(title), ext)
}
