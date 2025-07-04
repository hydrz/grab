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
	"time"

	"github.com/hydrz/grab/utils"
)

const downloadingSuffix = ".part"

// Downloader manages high-level download logic with support for HTTP range requests,
// resumable downloads, and multi-threaded downloads.
type Downloader struct {
	ctx    *Context
	mu     sync.RWMutex
	cancel context.CancelFunc
}

// NewDownloader creates a new Downloader instance with the provided context.
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

	filters := d.ctx.option.filtersForStreams(media.Streams)
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
		if err := d.downloadStreamWithRetry(ctx, stream); err != nil {
			d.ctx.logger.Error("Failed to download stream", "id", stream.ID, "error", err)
			if d.ctx.option.IgnoreErrors {
				continue
			}
			return fmt.Errorf("failed to download stream %s: %w", stream.ID, err)
		}
	}

	return nil
}

// downloadStreamWithRetry wraps downloadStream with retry logic and intelligent error handling
func (d *Downloader) downloadStreamWithRetry(ctx context.Context, stream Stream) error {
	maxRetries := d.ctx.option.RetryCount
	if maxRetries <= 0 {
		maxRetries = 1
	}

	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if attempt > 0 {
			d.ctx.logger.Info("Retrying download", "stream", stream.ID, "attempt", attempt+1, "maxRetries", maxRetries)
			// Exponential backoff with jitter
			backoffDuration := time.Duration(attempt*attempt) * time.Second
			if backoffDuration > 30*time.Second {
				backoffDuration = 30 * time.Second
			}

			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoffDuration):
			}
		}

		err := d.downloadStream(ctx, stream)
		if err == nil {
			return nil
		}

		lastErr = err
		d.ctx.logger.Warn("Download attempt failed", "stream", stream.ID, "attempt", attempt+1, "error", err)

		// Special handling for 416 Range Not Satisfiable - don't retry with range requests
		if strings.Contains(err.Error(), "416") {
			d.ctx.logger.Debug("Range request failed, trying without range", "stream", stream.ID)
			// Try one more time without range support
			err = d.downloadSingleThreadNoRange(ctx, stream)
			if err == nil {
				return nil
			}
			lastErr = err
		}

		// Don't retry on certain errors
		if isNonRetryableError(err) {
			d.ctx.logger.Debug("Non-retryable error, giving up", "error", err)
			break
		}
	}

	return fmt.Errorf("download failed after %d attempts: %w", maxRetries, lastErr)
}

// isNonRetryableError checks if an error should not be retried
func isNonRetryableError(err error) bool {
	if err == nil {
		return false
	}

	errStr := strings.ToLower(err.Error())
	nonRetryableErrors := []string{
		"404", "401", "403", "410", // HTTP client errors
		"invalid url", "no such host", "malformed",
		"context canceled", "context deadline exceeded",
	}

	for _, nonRetryable := range nonRetryableErrors {
		if strings.Contains(errStr, nonRetryable) {
			return true
		}
	}

	return false
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

// downloadStream dispatches the download logic based on stream type and server capabilities.
func (d *Downloader) downloadStream(ctx context.Context, stream Stream) error {
	outputDir := d.getOutputDir(stream)
	filename := d.getOutputFilename(stream)
	outputPath := filepath.Join(outputDir, filename)
	tempPath := outputPath + downloadingSuffix // Use .part suffix for incomplete downloads

	if !d.ctx.option.NoSkipExisting {
		if fi, err := os.Stat(outputPath); err == nil && fi.Size() == stream.Size {
			d.ctx.logger.Debug("File already exists, skipping", "path", outputPath)
			return nil
		}
	}

	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("failed to create output directory %s: %w", outputDir, err)
	}

	var err error
	if stream.Type == StreamTypeM3u8 {
		err = d.downloadM3U8Stream(ctx, stream, tempPath)
	} else {
		err = d.downloadSingleThread(ctx, stream, tempPath)
	}

	if err != nil {
		// Clean up empty or partial file on error
		if fi, statErr := os.Stat(tempPath); statErr == nil && fi.Size() == 0 {
			os.Remove(tempPath)
		}
		return err
	}

	// Rename .part file to final output name after successful download
	if renameErr := os.Rename(tempPath, outputPath); renameErr != nil {
		return fmt.Errorf("failed to rename temp file: %w", renameErr)
	}

	// Format conversion if requested
	if d.ctx.option.Format != "" && d.ctx.option.Format != stream.Format {
		d.ctx.logger.Info("Converting format", "from", stream.Format, "to", d.ctx.option.Format)
		convertedPath, convErr := convertFormat(outputPath, d.ctx.option.Format)
		if convErr != nil {
			return fmt.Errorf("format conversion failed: %w", convErr)
		}
		d.ctx.logger.Info("Format conversion completed", "output", convertedPath)

		// Remove original file after successful conversion
		if err := os.Remove(outputPath); err != nil {
			d.ctx.logger.Warn("Failed to remove original file after conversion", "file", outputPath, "error", err)
		}
	}

	return nil
}

// downloadSingleThread performs single-threaded download with resume capability
// downloadSingleThread performs single-threaded or multi-threaded (if supported) download with resume capability
func (d *Downloader) downloadSingleThread(ctx context.Context, stream Stream, tempPath string) error {
	// Step 1: Probe server for Range support and file size
	req := d.ctx.client.R().
		SetContext(ctx).
		SetDoNotParseResponse(true)
	req.Header = stream.Header.Clone()
	req.SetHeader("Range", "bytes=0-0")
	resp, err := req.Get(stream.URL)
	if err != nil {
		return fmt.Errorf("failed to probe server: %w", err)
	}
	defer resp.RawBody().Close()

	supportRange := false
	var totalSize int64 = stream.Size
	if resp.StatusCode() == http.StatusPartialContent {
		supportRange = true
		if cr := resp.Header().Get("Content-Range"); cr != "" {
			if parts := strings.Split(cr, "/"); len(parts) == 2 {
				if sz, err := strconv.ParseInt(parts[1], 10, 64); err == nil {
					totalSize = sz
				}
			}
		}
	} else if resp.StatusCode() == http.StatusOK {
		if cl := resp.Header().Get("Content-Length"); cl != "" {
			if sz, err := strconv.ParseInt(cl, 10, 64); err == nil {
				totalSize = sz
			}
		}
	} else {
		return fmt.Errorf("HTTP error: %s", resp.Status())
	}

	// Step 2: If not support range or Threads <= 1, fallback to original single-thread logic
	if !supportRange || d.ctx.option.Threads <= 1 || totalSize <= 0 {
		return d.downloadSingleThreadNoRange(ctx, stream)
	}

	// Step 3: Multi-threaded download
	threads := d.ctx.option.Threads
	chunkSize := totalSize / int64(threads)
	if chunkSize < 1 {
		chunkSize = totalSize
	}

	// Progress tracking
	progress := newProgress(totalSize, fmt.Sprintf("Downloading %s", stream.Title))
	if callback := d.ctx.GetProgressCallback(); callback != nil {
		progress.SetCallback(callback)
	}

	// Prepare temp files for each chunk
	tempFiles := make([]string, threads)
	var wg sync.WaitGroup
	errCh := make(chan error, threads)

	for i := 0; i < threads; i++ {
		start := int64(i) * chunkSize
		end := start + chunkSize - 1
		if i == threads-1 {
			end = totalSize - 1
		}
		tempFiles[i] = fmt.Sprintf("%s.part%d", tempPath, i)

		wg.Add(1)
		go func(idx int, start, end int64, tempFile string) {
			defer wg.Done()
			// Check if chunk already exists and is complete
			var existSize int64
			if fi, err := os.Stat(tempFile); err == nil {
				existSize = fi.Size()
			}
			if existSize >= (end - start + 1) {
				progress.Add(end - start + 1)
				return
			}

			req := d.ctx.client.R().
				SetContext(ctx).
				SetDoNotParseResponse(true)
			req.Header = stream.Header.Clone()
			req.SetHeader("Range", fmt.Sprintf("bytes=%d-%d", start+existSize, end))

			resp, err := req.Get(stream.URL)
			if err != nil {
				errCh <- fmt.Errorf("chunk %d request failed: %w", idx, err)
				return
			}
			defer resp.RawBody().Close()
			if resp.StatusCode() != http.StatusPartialContent && resp.StatusCode() != http.StatusOK {
				errCh <- fmt.Errorf("chunk %d HTTP error: %s", idx, resp.Status())
				return
			}

			// Open file for append
			f, err := os.OpenFile(tempFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
			if err != nil {
				errCh <- fmt.Errorf("chunk %d open file failed: %w", idx, err)
				return
			}
			defer f.Close()

			// Progress tracking for this chunk
			reader := progress.NewReader(resp.RawBody())
			if d.ctx.option.RateLimit > 0 {
				reader = utils.NewRateLimiter(reader, d.ctx.option.RateLimit/int64(threads))
			}
			defer func() {
				if c, ok := reader.(io.Closer); ok {
					c.Close()
				}
			}()

			_, err = d.copyWithContext(ctx, f, reader)
			if err != nil {
				errCh <- fmt.Errorf("chunk %d write failed: %w", idx, err)
				return
			}
		}(i, start, end, tempFiles[i])
	}

	wg.Wait()
	close(errCh)
	for e := range errCh {
		if e != nil {
			return e
		}
	}

	// Step 4: Merge chunks
	outFile, err := os.Create(tempPath)
	if err != nil {
		return fmt.Errorf("failed to create output file: %w", err)
	}
	defer outFile.Close()
	for i := 0; i < threads; i++ {
		f, err := os.Open(tempFiles[i])
		if err != nil {
			return fmt.Errorf("failed to open chunk %d: %w", i, err)
		}
		_, err = io.Copy(outFile, f)
		f.Close()
		if err != nil {
			return fmt.Errorf("failed to merge chunk %d: %w", i, err)
		}
		os.Remove(tempFiles[i])
	}

	progress.Finish()
	return nil
}

// downloadSingleThreadNoRange performs download without range requests
func (d *Downloader) downloadSingleThreadNoRange(ctx context.Context, stream Stream) error {
	outputDir := d.getOutputDir(stream)
	filename := d.getOutputFilename(stream)
	tempPath := filepath.Join(outputDir, filename) + downloadingSuffix

	req := d.ctx.client.R().
		SetContext(ctx).
		SetDoNotParseResponse(true)
	req.Header = stream.Header.Clone()

	resp, err := req.Get(stream.URL)
	if err != nil {
		return fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.RawBody().Close()

	if resp.StatusCode() != http.StatusOK {
		return fmt.Errorf("HTTP error: %s", resp.Status())
	}

	// Create output file (overwrite existing)
	file, err := os.Create(tempPath)
	if err != nil {
		return fmt.Errorf("failed to create output file: %w", err)
	}
	defer file.Close()

	// Get total size for progress tracking
	var totalSize int64 = stream.Size
	if contentLength := resp.Header().Get("Content-Length"); contentLength != "" {
		if size, err := strconv.ParseInt(contentLength, 10, 64); err == nil {
			totalSize = size
		}
	}

	// Progress tracking
	progress := newProgress(totalSize, fmt.Sprintf("Downloading %s", stream.Title))
	if callback := d.ctx.GetProgressCallback(); callback != nil {
		progress.SetCallback(callback)
	}

	reader := progress.NewReader(resp.RawBody())
	if d.ctx.option.RateLimit > 0 {
		reader = utils.NewRateLimiter(reader, d.ctx.option.RateLimit)
	}
	defer func() {
		if c, ok := reader.(io.Closer); ok {
			c.Close()
		}
	}()

	_, err = d.copyWithContext(ctx, file, reader)
	if err != nil {
		return fmt.Errorf("failed to write to output file: %w", err)
	}

	return nil
}

// downloadM3U8Stream handles M3U8 streams
func (d *Downloader) downloadM3U8Stream(ctx context.Context, stream Stream, tempPath string) error {
	data, err := d.processM3U8(stream)
	if err != nil {
		return fmt.Errorf("failed to process M3U8 stream: %w", err)
	}
	defer data.Close()

	// Create output file
	file, err := os.Create(tempPath)
	if err != nil {
		return fmt.Errorf("failed to create output file: %w", err)
	}
	defer file.Close()

	// Progress tracking
	progress := newProgress(stream.Size, fmt.Sprintf("Downloading %s", stream.Title))
	if callback := d.ctx.GetProgressCallback(); callback != nil {
		progress.SetCallback(callback)
	}

	reader := progress.NewReader(data)
	if d.ctx.option.RateLimit > 0 {
		reader = utils.NewRateLimiter(reader, d.ctx.option.RateLimit)
	}
	defer func() {
		if c, ok := reader.(io.Closer); ok {
			c.Close()
		}
	}()

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
