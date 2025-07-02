package grab

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/go-resty/resty/v2"
)

// chunkDownloader manages concurrent chunk downloads with resume capability.
// Not exported: only used internally for chunked download logic.
type chunkDownloader struct {
	client     *resty.Client
	url        string
	headers    http.Header
	totalSize  int64
	chunkSize  int64
	threads    int
	outputPath string
	ctx        context.Context
	progress   *progress
	logger     *slog.Logger
	mu         sync.RWMutex
	chunks     []*chunkInfo
	errors     []error
	errorMu    sync.Mutex
}

// chunkInfo represents a download chunk.
type chunkInfo struct {
	Index      int   `json:"index"`
	Start      int64 `json:"start"`
	End        int64 `json:"end"`
	Size       int64 `json:"size"`
	Downloaded int64 `json:"downloaded"`
	Completed  bool  `json:"completed"`
	Retries    int   `json:"retries"`
}

// Download implements multi-threaded chunk download with resume capability
func (cd *chunkDownloader) Download() error {
	// Check for existing partial download
	if err := cd.loadProgress(); err != nil {
		cd.initializeChunks()
	}

	// Validate chunks
	if err := cd.validateChunks(); err != nil {
		cd.logger.Warn("Invalid chunk state, reinitializing", "error", err)
		cd.initializeChunks()
	}

	// Pre-allocate output file
	if err := cd.preallocateFile(); err != nil {
		return fmt.Errorf("failed to preallocate file: %w", err)
	}

	// Open output file for random access writes
	output, err := os.OpenFile(cd.outputPath, os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("failed to open output file: %w", err)
	}
	defer output.Close()

	var wg sync.WaitGroup
	chunkChan := make(chan *chunkInfo, len(cd.chunks))
	semaphore := make(chan struct{}, cd.threads)

	// Initialize progress for already downloaded chunks
	for _, chunk := range cd.chunks {
		if chunk.Completed {
			cd.progress.Add(chunk.Size)
		} else {
			cd.progress.Add(chunk.Downloaded)
		}
	}

	// Queue incomplete chunks
	for _, chunk := range cd.chunks {
		if !chunk.Completed {
			chunkChan <- chunk
		}
	}
	close(chunkChan)

	// Start worker goroutines
	for i := 0; i < cd.threads; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cd.worker(chunkChan, output, semaphore)
		}()
	}

	// Progress saver goroutine
	progressDone := make(chan struct{})
	go cd.progressSaver(progressDone)

	wg.Wait()
	close(progressDone)

	if cd.ctx.Err() != nil {
		return cd.ctx.Err()
	}

	// Check for errors
	cd.errorMu.Lock()
	if len(cd.errors) > 0 {
		cd.errorMu.Unlock()
		return fmt.Errorf("chunk download failed: %v", cd.errors[0])
	}
	cd.errorMu.Unlock()

	// Verify all chunks completed
	if !cd.allChunksCompleted() {
		return fmt.Errorf("not all chunks completed successfully")
	}

	// Clean up progress file
	os.Remove(cd.outputPath + ".progress")
	return nil
}

// worker downloads chunks from the channel and writes directly to output file
func (cd *chunkDownloader) worker(chunkChan <-chan *chunkInfo, output *os.File, semaphore chan struct{}) {
	for chunk := range chunkChan {
		select {
		case <-cd.ctx.Done():
			return
		case semaphore <- struct{}{}:
		}

		if err := cd.downloadChunkWithRetry(chunk, output); err != nil {
			cd.logger.Error("Failed to download chunk", "chunk", chunk.Index, "error", err)
			cd.errorMu.Lock()
			cd.errors = append(cd.errors, fmt.Errorf("chunk %d: %w", chunk.Index, err))
			cd.errorMu.Unlock()
		}

		<-semaphore
	}
}

// downloadChunkWithRetry downloads a chunk with retry logic
func (cd *chunkDownloader) downloadChunkWithRetry(chunk *chunkInfo, output *os.File) error {
	maxRetries := 3

	for attempt := 0; attempt <= maxRetries; attempt++ {
		select {
		case <-cd.ctx.Done():
			return cd.ctx.Err()
		default:
		}

		if attempt > 0 {
			cd.logger.Debug("Retrying chunk download", "chunk", chunk.Index, "attempt", attempt+1)
			backoff := time.Duration(attempt) * time.Second
			if backoff > 10*time.Second {
				backoff = 10 * time.Second
			}

			select {
			case <-cd.ctx.Done():
				return cd.ctx.Err()
			case <-time.After(backoff):
			}
		}

		err := cd.downloadChunkDirect(chunk, output)
		if err == nil {
			return nil
		}

		chunk.Retries++
		cd.logger.Warn("Chunk download failed", "chunk", chunk.Index, "attempt", attempt+1, "error", err)

		// Don't retry on certain errors
		if isNonRetryableError(err) {
			return err
		}
	}

	return fmt.Errorf("chunk download failed after %d attempts", maxRetries+1)
}

// downloadChunkDirect downloads a single chunk and writes directly to the output file at the correct offset
func (cd *chunkDownloader) downloadChunkDirect(chunk *chunkInfo, output *os.File) error {
	rangeStart := chunk.Start + chunk.Downloaded
	if rangeStart > chunk.End {
		chunk.Completed = true
		chunk.Downloaded = chunk.Size
		return nil
	}

	client := cd.client
	rangeHeader := fmt.Sprintf("bytes=%d-%d", rangeStart, chunk.End)

	req := client.R().
		SetContext(cd.ctx).
		SetHeader("Range", rangeHeader).
		SetDoNotParseResponse(true)

	for key, values := range cd.headers {
		for _, value := range values {
			req.SetHeader(key, value)
		}
	}

	resp, err := req.Get(cd.url)
	if err != nil {
		return fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.RawBody().Close()

	if resp.StatusCode() != http.StatusPartialContent && resp.StatusCode() != http.StatusOK {
		return fmt.Errorf("HTTP error: %s", resp.Status())
	}

	buf := make([]byte, 128*1024) // Larger buffer for better throughput
	offset := rangeStart
	chunkDownloaded := chunk.Downloaded

	for {
		select {
		case <-cd.ctx.Done():
			return cd.ctx.Err()
		default:
		}

		nr, er := resp.RawBody().Read(buf)
		if nr > 0 {
			nw, ew := output.WriteAt(buf[:nr], offset)
			if ew != nil {
				return fmt.Errorf("failed to write to file: %w", ew)
			}
			if nw != nr {
				return fmt.Errorf("incomplete write: expected %d, wrote %d", nr, nw)
			}

			offset += int64(nw)
			chunkDownloaded += int64(nw)

			// Update progress atomically
			cd.mu.Lock()
			progressDelta := chunkDownloaded - chunk.Downloaded
			chunk.Downloaded = chunkDownloaded
			cd.mu.Unlock()

			if progressDelta > 0 {
				cd.progress.Add(progressDelta)
			}
		}

		if er != nil {
			if er != io.EOF {
				return fmt.Errorf("failed to read response: %w", er)
			}
			break
		}
	}

	cd.mu.Lock()
	chunk.Completed = chunk.Downloaded >= chunk.Size
	cd.mu.Unlock()

	return nil
}

// preallocateFile creates and preallocates the output file to the expected size
func (cd *chunkDownloader) preallocateFile() error {
	file, err := os.OpenFile(cd.outputPath, os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer file.Close()

	// Check current file size
	info, err := file.Stat()
	if err != nil {
		return err
	}

	// Only truncate if file size doesn't match expected size
	if info.Size() != cd.totalSize {
		if err := file.Truncate(cd.totalSize); err != nil {
			return fmt.Errorf("failed to preallocate file: %w", err)
		}
	}

	return nil
}

// validateChunks ensures chunk state is consistent
func (cd *chunkDownloader) validateChunks() error {
	if len(cd.chunks) == 0 {
		return fmt.Errorf("no chunks found")
	}

	var totalSize int64
	for _, chunk := range cd.chunks {
		if chunk.Start < 0 || chunk.End < chunk.Start {
			return fmt.Errorf("invalid chunk range: %d-%d", chunk.Start, chunk.End)
		}
		if chunk.Downloaded < 0 || chunk.Downloaded > chunk.Size {
			return fmt.Errorf("invalid downloaded size for chunk %d", chunk.Index)
		}
		totalSize += chunk.Size
	}

	if totalSize != cd.totalSize {
		return fmt.Errorf("chunk total size mismatch: expected %d, got %d", cd.totalSize, totalSize)
	}

	return nil
}

// allChunksCompleted checks if all chunks are completed
func (cd *chunkDownloader) allChunksCompleted() bool {
	cd.mu.RLock()
	defer cd.mu.RUnlock()

	for _, chunk := range cd.chunks {
		if !chunk.Completed {
			return false
		}
	}
	return true
}

// progressSaver periodically saves progress to disk
func (cd *chunkDownloader) progressSaver(done <-chan struct{}) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			cd.saveProgress()
		}
	}
}

// saveProgress saves current progress to disk
func (cd *chunkDownloader) saveProgress() {
	cd.mu.RLock()
	defer cd.mu.RUnlock()

	progressFile := cd.outputPath + ".progress"

	type progressState struct {
		Chunks    []*chunkInfo `json:"chunks"`
		TotalSize int64        `json:"totalSize"`
		URL       string       `json:"url"`
		Timestamp time.Time    `json:"timestamp"`
	}

	state := progressState{
		Chunks:    cd.chunks,
		TotalSize: cd.totalSize,
		URL:       cd.url,
		Timestamp: time.Now(),
	}

	file, err := os.Create(progressFile)
	if err != nil {
		cd.logger.Warn("Failed to create progress file", "error", err)
		return
	}
	defer file.Close()

	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(state); err != nil {
		cd.logger.Warn("Failed to save progress", "error", err)
	}
}

// initializeChunks creates chunk information for download
func (cd *chunkDownloader) initializeChunks() {
	numChunks := int((cd.totalSize + cd.chunkSize - 1) / cd.chunkSize)
	cd.chunks = make([]*chunkInfo, numChunks)

	for i := 0; i < numChunks; i++ {
		start := int64(i) * cd.chunkSize
		end := start + cd.chunkSize - 1
		if end >= cd.totalSize {
			end = cd.totalSize - 1
		}

		cd.chunks[i] = &chunkInfo{
			Index: i,
			Start: start,
			End:   end,
			Size:  end - start + 1,
		}
	}
}

// loadProgress attempts to load existing download progress from a JSON file.
// If the progress file does not exist or is invalid, returns an error and triggers a fresh download.
// The progress file records the state of each chunk for resuming interrupted downloads.
func (cd *chunkDownloader) loadProgress() error {
	progressFile := cd.outputPath + ".progress"

	file, err := os.Open(progressFile)
	if err != nil {
		return fmt.Errorf("no progress file found")
	}
	defer file.Close()

	type progressState struct {
		Chunks    []*chunkInfo `json:"chunks"`
		TotalSize int64        `json:"totalSize"`
		URL       string       `json:"url"`
		Timestamp time.Time    `json:"timestamp"`
	}

	var state progressState
	decoder := json.NewDecoder(file)
	if err := decoder.Decode(&state); err != nil {
		return fmt.Errorf("failed to decode progress file: %w", err)
	}

	// Validate progress file
	if len(state.Chunks) == 0 {
		return fmt.Errorf("progress file is empty")
	}

	if state.TotalSize != cd.totalSize {
		return fmt.Errorf("progress file size mismatch: expected %d, got %d", cd.totalSize, state.TotalSize)
	}

	if state.URL != cd.url {
		return fmt.Errorf("progress file URL mismatch")
	}

	// Check if progress file is too old (older than 7 days)
	if time.Since(state.Timestamp) > 7*24*time.Hour {
		return fmt.Errorf("progress file is too old")
	}

	cd.chunks = state.Chunks
	cd.logger.Info("Loaded progress file", "chunks", len(cd.chunks), "timestamp", state.Timestamp)
	return nil
}
