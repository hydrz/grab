package grab

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/go-resty/resty/v2"
	"github.com/hydrz/grab/utils"
)

// Downloader provides high-level download management with support for HTTP range requests, resumable and multi-threaded downloads.
// ProgressCallback, if set, will be called with (current, total) for each stream/segment.
type Downloader struct {
	ctx              context.Context
	option           Option
	client           *resty.Client
	logger           *slog.Logger
	mu               sync.RWMutex
	ProgressCallback func(current, total int)
}

// NewDownloader creates a new Downloader instance with merged options and a dedicated HTTP client.
// Optionally, set ProgressCallback after creation for progress reporting.
func NewDownloader(opts ...Option) *Downloader {
	opt := *DefaultOptions // Deep copy to avoid mutating global defaultOptions
	for _, o := range opts {
		opt.Combine(o)
	}
	return &Downloader{
		ctx:    opt.Ctx,
		option: opt,
		client: opt.Client(),
		logger: opt.Logger(),
	}
}

// Download downloads all streams from the extracted media for the given URL.
// If ExtractOnly is set, only prints media info without downloading.
func (d *Downloader) Download(url string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if !utils.IsValidURL(url) {
		return ErrInvalidURL
	}

	extractor, err := FindExtractor(url)
	if err != nil {
		return err
	}

	medias, err := extractor.Extract(url, d.option)
	if err != nil {
		return err
	}

	if d.option.ExtractOnly {
		d.printMediaInfo(medias)
		return nil
	}

	for _, media := range medias {
		d.logger.Debug("Downloading media", "title", media.Title)
		if err := d.downloadMedia(media); err != nil {
			d.logger.Error("Failed to download media", "title", media.Title, "error", err)
			if d.option.IgnoreErrors {
				continue
			}
			return fmt.Errorf("failed to download media %s: %w", media.Title, err)
		}
	}

	return nil
}

// downloadMedia downloads all streams for a given media, applying filters and error handling.
func (d *Downloader) downloadMedia(media Media) error {
	if len(media.Streams) == 0 {
		return fmt.Errorf("no streams available for media %s", media.Title)
	}

	filters := d.option.FiltersForStreams(media.Streams)
	for _, stream := range media.Streams {
		if d.shouldSkipStream(stream, filters) {
			continue
		}

		d.logger.Debug("Downloading stream", "id", stream.ID, "type", stream.Type, "quality", stream.Quality)
		if err := d.downloadStream(stream); err != nil {
			d.logger.Error("Failed to download stream", "id", stream.ID, "error", err)
			if d.option.IgnoreErrors {
				continue
			}
			return fmt.Errorf("failed to download stream %s: %w", stream.ID, err)
		}
	}

	return nil
}

// shouldSkipStream returns true if the stream should be skipped according to filters.
// If any filter does not match, the stream will be skipped.
// If no filters are set, all streams are accepted.
func (d *Downloader) shouldSkipStream(stream Stream, filters []Filter) bool {
	if len(filters) == 0 {
		return false
	}
	for _, filter := range filters {
		if !filter.Filter(stream) {
			d.logger.Debug("Skipping stream", "id", stream.ID, "reason", filter)
			return true
		}
	}
	return false
}

// downloadStream dispatches the download logic based on stream type.
func (d *Downloader) downloadStream(stream Stream) error {
	outputDir := d.getOutputDir(stream)
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("failed to create output directory %s: %w", outputDir, err)
	}
	filename := d.getOutputFilename(stream)
	outputPath := filepath.Join(outputDir, filename)

	if d.option.SkipExisting {
		if fi, err := os.Stat(outputPath); err == nil && fi.Size() > 0 {
			d.logger.Debug("File already exists, skipping", "path", outputPath)
			return nil
		}
	}

	if stream.Type == StreamTypeM3u8 {
		return d.downloadM3U8(stream, outputPath)
	}
	return d.downloadDirectWithResume(stream, outputPath)
}

// downloadDirectWithResume downloads a file with HTTP range support and resumable capability.
// Uses chunked multi-threaded download if enabled and supported by the server.
func (d *Downloader) downloadDirectWithResume(stream Stream, outputPath string) error {
	d.client.Header = utils.MergeHeader(d.client.Header, stream.Headers)

	resp, err := d.client.R().Head(stream.URL)
	if err != nil {
		return fmt.Errorf("failed to get file info: %w", err)
	}
	contentLength := resp.Header().Get("Content-Length")
	supportRange := strings.Contains(strings.ToLower(resp.Header().Get("Accept-Ranges")), "bytes")
	var totalSize int64
	if contentLength != "" {
		totalSize, _ = strconv.ParseInt(contentLength, 10, 64)
	}
	// If Content-Length is missing, fallback to stream.Size if available
	if totalSize == 0 && stream.Size > 0 {
		totalSize = stream.Size
	}

	if supportRange && d.option.Threads > 1 && d.option.ChunkSize > 0 && totalSize > 0 {
		return d.downloadInChunks(stream, outputPath, totalSize)
	}
	return d.downloadSingleWithResume(stream, outputPath, totalSize, supportRange)
}

// downloadSingleWithResume downloads a file with resume support (single thread).
func (d *Downloader) downloadSingleWithResume(stream Stream, outputPath string, totalSize int64, supportRange bool) error {
	var start int64 = 0
	var file *os.File
	var err error

	if fi, errStat := os.Stat(outputPath); errStat == nil {
		start = fi.Size()
		if start > 0 && supportRange {
			file, err = os.OpenFile(outputPath, os.O_WRONLY|os.O_APPEND, 0644)
		} else {
			file, err = os.Create(outputPath)
			start = 0
		}
	} else {
		file, err = os.Create(outputPath)
	}
	if err != nil {
		return fmt.Errorf("failed to open output file: %w", err)
	}
	defer file.Close()

	// Progress bar is handled in main.go via ProgressCallback, so here only call ProgressCallback if set.
	if d.ProgressCallback != nil && totalSize > 0 {
		d.ProgressCallback(int(start), int(totalSize))
	}

	req := d.client.R().SetDoNotParseResponse(true)
	req.Header = utils.MergeHeader(req.Header, stream.Headers)
	if supportRange && start > 0 {
		req.SetHeader("Range", fmt.Sprintf("bytes=%d-", start))
	}

	resp, err := req.Get(stream.URL)
	if err != nil {
		return fmt.Errorf("failed to download: %w", err)
	}
	defer resp.RawBody().Close()

	var written int64 = start
	buf := make([]byte, 32*1024)
	for {
		nr, er := resp.RawBody().Read(buf)
		if nr > 0 {
			nw, ew := file.Write(buf[0:nr])
			if nw > 0 {
				written += int64(nw)
				if d.ProgressCallback != nil && totalSize > 0 {
					d.ProgressCallback(int(written), int(totalSize))
				}
			}
			if ew != nil {
				os.Remove(outputPath)
				return fmt.Errorf("failed to write file: %w", ew)
			}
			if nr != nw {
				os.Remove(outputPath)
				return fmt.Errorf("short write")
			}
		}
		if er != nil {
			if er == io.EOF {
				break
			}
			os.Remove(outputPath)
			return fmt.Errorf("failed to read response: %w", er)
		}
	}

	d.logger.Debug("Download completed", "path", outputPath)
	return nil
}

// downloadInChunks performs multi-threaded chunked download with resume support.
func (d *Downloader) downloadInChunks(stream Stream, outputPath string, totalSize int64) error {
	chunkSize := d.option.ChunkSize
	threads := d.option.Threads
	if chunkSize <= 0 {
		chunkSize = 1024 * 1024 // 1MB default
	}
	if threads <= 1 {
		threads = 1
	}

	type chunkInfo struct {
		Index int
		Start int64
		End   int64
		Path  string
	}
	var chunks []chunkInfo
	for i, offset := 0, int64(0); offset < totalSize; i, offset = i+1, offset+chunkSize {
		end := offset + chunkSize - 1
		if end >= totalSize {
			end = totalSize - 1
		}
		chunks = append(chunks, chunkInfo{
			Index: i,
			Start: offset,
			End:   end,
			Path:  fmt.Sprintf("%s.part%d", outputPath, i),
		})
	}

	var wg sync.WaitGroup
	errCh := make(chan error, len(chunks))
	progressCh := make(chan int64, len(chunks))
	sem := make(chan struct{}, threads)

	var totalWritten int64 = 0
	if d.ProgressCallback != nil && totalSize > 0 {
		d.ProgressCallback(int(totalWritten), int(totalSize))
	}

	for _, chunk := range chunks {
		wg.Add(1)
		go func(chunk chunkInfo) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			if fi, err := os.Stat(chunk.Path); err == nil && fi.Size() == chunk.End-chunk.Start+1 {
				progressCh <- fi.Size()
				return
			}
			req := d.client.R().
				SetHeader("Range", fmt.Sprintf("bytes=%d-%d", chunk.Start, chunk.End)).
				SetDoNotParseResponse(true)
			req.Header = utils.MergeHeader(req.Header, stream.Headers)
			resp, err := req.Get(stream.URL)
			if err != nil {
				errCh <- fmt.Errorf("chunk %d: %w", chunk.Index, err)
				return
			}
			defer resp.RawBody().Close()
			f, err := os.Create(chunk.Path)
			if err != nil {
				errCh <- fmt.Errorf("chunk %d: %w", chunk.Index, err)
				return
			}
			defer f.Close()
			n, err := io.Copy(f, resp.RawBody())
			if err != nil {
				errCh <- fmt.Errorf("chunk %d: %w", chunk.Index, err)
				return
			}
			progressCh <- n
		}(chunk)
	}

	go func() {
		for n := range progressCh {
			totalWritten += n
			if d.ProgressCallback != nil && totalSize > 0 {
				d.ProgressCallback(int(totalWritten), int(totalSize))
			}
		}
	}()

	wg.Wait()
	close(errCh)
	close(progressCh)

	for err := range errCh {
		if err != nil {
			for _, chunk := range chunks {
				os.Remove(chunk.Path)
			}
			return err
		}
	}

	out, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("failed to create output file: %w", err)
	}
	defer out.Close()
	for _, chunk := range chunks {
		f, err := os.Open(chunk.Path)
		if err != nil {
			return fmt.Errorf("failed to open chunk: %w", err)
		}
		_, err = io.Copy(out, f)
		f.Close()
		if err != nil {
			return fmt.Errorf("failed to merge chunk: %w", err)
		}
		os.Remove(chunk.Path)
	}

	d.logger.Debug("Download completed", "path", outputPath)
	return nil
}

// getOutputDir determines the output directory for a stream, considering SaveAs and OutputPath.
func (d *Downloader) getOutputDir(stream Stream) string {
	if stream.SaveAs != "" {
		dir := filepath.Dir(stream.SaveAs)
		if filepath.IsAbs(dir) {
			return dir
		}
		return filepath.Join(d.option.OutputPath, dir)
	}
	return d.option.OutputPath
}

// getOutputFilename determines the output filename for a stream, considering OutputName and SaveAs.
func (d *Downloader) getOutputFilename(stream Stream) string {
	if d.option.OutputName != "" {
		ext := utils.FileExtension(d.option.OutputName)
		if ext == "" {
			ext = "." + stream.Format
		}
		name := d.option.OutputName
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

// downloadM3U8 delegates M3U8 stream download to the M3U8Downloader and passes progress callback.
func (d *Downloader) downloadM3U8(stream Stream, outputPath string) error {
	d.logger.Debug("Downloading M3U8 stream", "url", stream.URL, "output", outputPath)

	client := d.client.Clone()
	client.Header = utils.MergeHeader(client.Header, stream.Headers)
	m3u8Downloader := NewM3U8Downloader(client, d.option)
	m3u8Downloader.ProgressCallback = d.ProgressCallback

	return m3u8Downloader.Download(stream.URL, outputPath)
}

// printMediaInfo prints extracted media information in a plain text format for inspection or debugging.
func (d *Downloader) printMediaInfo(medias []Media) {
	if len(medias) == 0 {
		fmt.Println("No media information available.")
		return
	}
	for _, media := range medias {
		fmt.Printf("Title: %s\n", media.Title)
		if len(media.Streams) == 0 {
			fmt.Println("  No streams available.")
			continue
		}
		for i, stream := range media.Streams {
			fmt.Printf("  Stream #%d:\n", i+1)
			fmt.Printf("    Type:     %s\n", stream.Type)
			fmt.Printf("    Quality:  %s\n", stream.Quality)
			fmt.Printf("    Format:   %s\n", stream.Format)
			fmt.Printf("    Size:     %s\n", utils.FormatBytes(stream.Size))
			fmt.Printf("    Duration: %s\n", utils.FormatDuration(stream.Duration))
			fmt.Printf("    URL:      %s\n", stream.URL)
		}
		fmt.Println()
	}
}
