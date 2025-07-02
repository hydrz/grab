package grab

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"

	"github.com/go-resty/resty/v2"
)

// ChunkDownloader manages concurrent chunk downloads with resume capability
type ChunkDownloader struct {
	client     *resty.Client
	url        string
	headers    http.Header
	totalSize  int64
	chunkSize  int64
	threads    int
	outputPath string
	ctx        context.Context
	progress   *Progress
	mu         sync.RWMutex
	chunks     []*ChunkInfo
}

// ChunkInfo represents a download chunk
type ChunkInfo struct {
	Index      int
	Start      int64
	End        int64
	Size       int64
	Downloaded int64
	Completed  bool
}

// Download implements multi-threaded chunk download with resume capability
func (cd *ChunkDownloader) Download() error {
	// Check for existing partial download
	if err := cd.loadProgress(); err != nil {
		cd.initializeChunks()
	}

	// Open output file for random access writes
	output, err := os.OpenFile(cd.outputPath, os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("failed to open output file: %w", err)
	}
	defer output.Close()

	var wg sync.WaitGroup
	chunkChan := make(chan *ChunkInfo, len(cd.chunks))

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
			cd.worker(chunkChan, output)
		}()
	}

	wg.Wait()

	if cd.ctx.Err() != nil {
		return cd.ctx.Err()
	}

	// Clean up progress file
	os.Remove(cd.outputPath + ".progress")
	return nil
}

// worker downloads chunks from the channel and writes directly to output file
func (cd *ChunkDownloader) worker(chunkChan <-chan *ChunkInfo, output *os.File) {
	for chunk := range chunkChan {
		select {
		case <-cd.ctx.Done():
			return
		default:
		}

		if err := cd.downloadChunkDirect(chunk, output); err != nil {
			fmt.Printf("Failed to download chunk %d: %v\n", chunk.Index, err)
			continue
		}
	}
}

// downloadChunkDirect downloads a single chunk and writes directly to the output file at the correct offset
func (cd *ChunkDownloader) downloadChunkDirect(chunk *ChunkInfo, output *os.File) error {
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
				return ew
			}
			offset += int64(nw)
			chunk.Downloaded += int64(nw)
			cd.progress.Add(int64(nw))
		}
		if er != nil {
			if er != io.EOF {
				return er
			}
			break
		}
	}

	chunk.Completed = chunk.Downloaded >= chunk.Size
	return nil
}

// initializeChunks creates chunk information for download
func (cd *ChunkDownloader) initializeChunks() {
	numChunks := int((cd.totalSize + cd.chunkSize - 1) / cd.chunkSize)
	cd.chunks = make([]*ChunkInfo, numChunks)

	for i := 0; i < numChunks; i++ {
		start := int64(i) * cd.chunkSize
		end := start + cd.chunkSize - 1
		if end >= cd.totalSize {
			end = cd.totalSize - 1
		}

		cd.chunks[i] = &ChunkInfo{
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
func (cd *ChunkDownloader) loadProgress() error {
	progressFile := cd.outputPath + ".progress"

	file, err := os.Open(progressFile)
	if err != nil {
		return fmt.Errorf("no progress file found")
	}
	defer file.Close()

	type progressState struct {
		Chunks []*ChunkInfo
	}

	var state progressState
	decoder := json.NewDecoder(file)
	if err := decoder.Decode(&state); err != nil {
		return fmt.Errorf("failed to decode progress file: %w", err)
	}

	// Validate and restore chunk state
	if len(state.Chunks) == 0 {
		return fmt.Errorf("progress file is empty")
	}
	cd.chunks = state.Chunks
	return nil
}
