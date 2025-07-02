package grab

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-resty/resty/v2"
	"github.com/grafov/m3u8"
)

// segmentInfo holds information and cached data for a single segment.
type segmentInfo struct {
	URI      string
	Duration float64
	Key      *m3u8.Key
	Headers  http.Header
	Retries  int
	data     []byte // Cached segment data
}

// segmentData is used for concurrent segment download coordination.
type segmentData struct {
	index int
	data  []byte
	err   error
}

// m3U8Reader implements concurrent segment downloading with memory pooling.
type m3U8Reader struct {
	segments      []*segmentInfo
	currentIdx    int
	currentReader io.ReadCloser
	tempDir       string   // Temporary directory for segment files
	cleanup       []string // Files to cleanup
	mu            sync.Mutex
	client        *resty.Client
	maxRetries    int
	retryDelay    time.Duration

	// Optimization fields
	bufferPool   sync.Pool
	segmentChan  chan *segmentData
	errorChan    chan error
	workers      int
	prefetchSize int
	closed       bool         // Track if reader is closed
	closeMu      sync.RWMutex // Protect closed flag
}

// processM3U8 handles M3U8 streams with zero-copy optimization and encryption support.
// Returns a ReadCloser that streams segments on-demand without loading everything into memory.
func (d *Downloader) processM3U8(stream Stream) (io.ReadCloser, error) {
	if stream.Type != StreamTypeM3u8 {
		return nil, nil // Not an M3U8 stream
	}

	playlist, listType, err := d.parsePlaylist(stream)
	if err != nil {
		return nil, fmt.Errorf("failed to parse playlist: %w", err)
	}

	switch listType {
	case m3u8.MEDIA:
		return d.processMediaPlaylist(playlist.(*m3u8.MediaPlaylist), stream)
	case m3u8.MASTER:
		return d.processMasterPlaylist(playlist.(*m3u8.MasterPlaylist), stream)
	default:
		return nil, fmt.Errorf("unsupported playlist type: %d", listType)
	}
}

// parsePlaylist fetches and parses an M3U8 playlist from the given URL.
func (d *Downloader) parsePlaylist(stream Stream) (m3u8.Playlist, m3u8.ListType, error) {
	playlistURL := stream.URL
	req := d.ctx.client.R().
		SetContext(d.ctx.ctx).
		SetDoNotParseResponse(true)
	req.Header = stream.Header.Clone()

	resp, err := req.Get(playlistURL)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to fetch playlist: %w", err)
	}
	defer resp.RawBody().Close()

	if resp.StatusCode() != http.StatusOK {
		return nil, 0, fmt.Errorf("HTTP error: %s, URL: %s", resp.Status(), playlistURL)
	}

	playlist, listType, err := m3u8.DecodeFrom(resp.RawBody(), true)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to decode playlist: %w", err)
	}

	return playlist, listType, nil
}

// processMediaPlaylist creates an optimized reader for media playlist segments.
func (d *Downloader) processMediaPlaylist(playlist *m3u8.MediaPlaylist, stream Stream) (io.ReadCloser, error) {
	baseURL, err := url.Parse(stream.URL)
	if err != nil {
		return nil, fmt.Errorf("invalid base URL: %w", err)
	}

	segments := make([]*segmentInfo, 0, len(playlist.Segments))
	var currentKey *m3u8.Key

	for _, segment := range playlist.Segments {
		if segment == nil {
			continue
		}
		if segment.Key != nil {
			currentKey = segment.Key
		}
		segmentURL, err := baseURL.Parse(segment.URI)
		if err != nil {
			d.ctx.logger.Warn("Invalid segment URI", "uri", segment.URI, "error", err)
			continue
		}
		segments = append(segments, &segmentInfo{
			URI:      segmentURL.String(),
			Duration: segment.Duration,
			Key:      currentKey,
			Headers:  stream.Header,
		})
	}

	if len(segments) == 0 {
		return nil, fmt.Errorf("no valid segments found in playlist")
	}

	tempDir, err := os.MkdirTemp("", "grab_m3u8_*")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp directory: %w", err)
	}

	workers := min(d.ctx.option.Threads, len(segments))
	if workers <= 0 {
		workers = min(4, len(segments))
	}
	prefetchSize := min(workers*2, 10)

	reader := &m3U8Reader{
		segments:     segments,
		tempDir:      tempDir,
		cleanup:      make([]string, 0),
		client:       d.ctx.client,
		maxRetries:   max(d.ctx.option.RetryCount, 3),
		retryDelay:   time.Second,
		workers:      workers,
		prefetchSize: prefetchSize,
		segmentChan:  make(chan *segmentData, prefetchSize),
		errorChan:    make(chan error, 1),
		bufferPool: sync.Pool{
			New: func() interface{} {
				return make([]byte, 0, 1024*1024) // 1MB initial capacity
			},
		},
	}

	reader.startWorkers()
	return reader, nil
}

// processMasterPlaylist selects the best quality stream from master playlist.
func (d *Downloader) processMasterPlaylist(playlist *m3u8.MasterPlaylist, stream Stream) (io.ReadCloser, error) {
	if len(playlist.Variants) == 0 {
		return nil, fmt.Errorf("no variants found in master playlist")
	}

	var selectedVariant *m3u8.Variant
	maxBandwidth := uint32(0)
	for _, variant := range playlist.Variants {
		if variant != nil && variant.Bandwidth > maxBandwidth {
			maxBandwidth = variant.Bandwidth
			selectedVariant = variant
		}
	}
	if selectedVariant == nil {
		return nil, fmt.Errorf("no suitable variant found")
	}

	baseURL, err := url.Parse(stream.URL)
	if err != nil {
		return nil, fmt.Errorf("invalid base URL: %w", err)
	}
	variantURL, err := baseURL.Parse(selectedVariant.URI)
	if err != nil {
		return nil, fmt.Errorf("invalid variant URI: %w", err)
	}

	variantStream := Stream{
		ID:      stream.ID + "_variant",
		Title:   stream.Title,
		Type:    StreamTypeM3u8,
		URL:     variantURL.String(),
		Format:  stream.Format,
		Quality: selectedVariant.Resolution,
		Header:  stream.Header,
	}
	return d.processM3U8(variantStream)
}

// startWorkers launches background goroutines to download segments concurrently.
func (r *m3U8Reader) startWorkers() {
	for i := 0; i < r.workers; i++ {
		go r.downloadWorker()
	}
	go r.prefetchCoordinator()
}

// prefetchCoordinator manages which segments to download next.
// It only handles initial prefetching and then closes the channel.
func (r *m3U8Reader) prefetchCoordinator() {
	defer close(r.segmentChan)

	// Only prefetch initial segments, let triggerPrefetch handle the rest
	for i := 0; i < len(r.segments) && i < r.prefetchSize; i++ {
		select {
		case r.segmentChan <- &segmentData{index: i}:
		case <-r.errorChan:
			return
		}
	}
}

// downloadWorker downloads segments concurrently.
func (r *m3U8Reader) downloadWorker() {
	for segData := range r.segmentChan {
		if segData.index >= len(r.segments) {
			continue
		}
		segment := r.segments[segData.index]
		data, err := r.downloadSegmentToMemory(segment)
		if err != nil {
			segData.err = err
			select {
			case r.errorChan <- err:
			default:
			}
			continue
		}
		segData.data = data
		r.segments[segData.index].data = data
	}
}

// downloadSegmentToMemory downloads a segment directly to memory with optimizations.
func (r *m3U8Reader) downloadSegmentToMemory(segment *segmentInfo) ([]byte, error) {
	var lastErr error
	for attempt := 0; attempt < r.maxRetries; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(attempt) * r.retryDelay)
		}
		data, err := r.fetchSegmentData(segment)
		if err == nil {
			return data, nil
		}
		lastErr = err
		segment.Retries++
		if isNonRetryableError(err) {
			break
		}
	}
	return nil, fmt.Errorf("failed to download segment after %d attempts: %w", r.maxRetries, lastErr)
}

// fetchSegmentData downloads segment data directly to memory.
func (r *m3U8Reader) fetchSegmentData(segment *segmentInfo) ([]byte, error) {
	req := r.client.R().
		SetDoNotParseResponse(true)
	if segment.Headers != nil {
		req.Header = segment.Headers.Clone()
	}

	resp, err := req.Get(segment.URI)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.RawBody().Close()
	if resp.StatusCode() != http.StatusOK {
		return nil, fmt.Errorf("HTTP error: %s", resp.Status())
	}

	data, err := io.ReadAll(resp.RawBody())
	if err != nil {
		return nil, fmt.Errorf("failed to read segment data: %w", err)
	}

	if segment.Key != nil && segment.Key.Method == "AES-128" {
		decrypted, err := r.decryptSegmentData(data, segment.Key)
		if err != nil {
			return nil, fmt.Errorf("failed to decrypt segment: %w", err)
		}
		return decrypted, nil
	}
	return data, nil
}

// decryptSegmentData decrypts segment data in memory.
func (r *m3U8Reader) decryptSegmentData(data []byte, key *m3u8.Key) ([]byte, error) {
	keyData, err := r.downloadKeyWithRetry(key.URI)
	if err != nil {
		return nil, fmt.Errorf("failed to download encryption key: %w", err)
	}
	block, err := aes.NewCipher(keyData)
	if err != nil {
		return nil, fmt.Errorf("failed to create AES cipher: %w", err)
	}
	iv := make([]byte, aes.BlockSize)
	if key.IV != "" {
		ivStr := strings.TrimPrefix(key.IV, "0x")
		if len(ivStr) != 32 {
			return nil, fmt.Errorf("invalid IV length: %d", len(ivStr))
		}
		for i := 0; i < 16; i++ {
			b, err := strconv.ParseUint(ivStr[i*2:(i+1)*2], 16, 8)
			if err != nil {
				return nil, fmt.Errorf("invalid IV format: %w", err)
			}
			iv[i] = byte(b)
		}
	}
	decryptor := cipher.NewCBCDecrypter(block, iv)
	if len(data)%aes.BlockSize != 0 {
		return nil, fmt.Errorf("data length not aligned to block size")
	}
	decrypted := make([]byte, len(data))
	decryptor.CryptBlocks(decrypted, data)
	return removePKCS7Padding(decrypted), nil
}

// Read implements io.Reader with zero-copy segment streaming.
func (r *m3U8Reader) Read(p []byte) (n int, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for {
		if r.currentReader != nil {
			n, err = r.currentReader.Read(p)
			if err == nil || (err != io.EOF && n > 0) {
				return n, err
			}
			r.currentReader.Close()
			r.currentReader = nil
		}
		if r.currentIdx >= len(r.segments) {
			return 0, io.EOF
		}
		segment := r.segments[r.currentIdx]
		r.currentIdx++
		var reader io.ReadCloser
		if segment.data != nil {
			reader = io.NopCloser(bytes.NewReader(segment.data))
		} else {
			reader, err = r.openSegmentWithRetry(segment)
			if err != nil {
				return 0, fmt.Errorf("failed to open segment %d: %w", r.currentIdx-1, err)
			}
		}
		r.currentReader = reader
		r.triggerPrefetch()
	}
}

// triggerPrefetch requests downloading of upcoming segments.
// Uses a separate mechanism to avoid sending to closed channels.
func (r *m3U8Reader) triggerPrefetch() {
	start := r.currentIdx
	end := min(start+r.prefetchSize, len(r.segments))

	go func() {
		for i := start; i < end; i++ {
			if r.segments[i].data == nil {
				// Check if reader is closed before proceeding
				r.closeMu.RLock()
				if r.closed {
					r.closeMu.RUnlock()
					return
				}
				r.closeMu.RUnlock()

				// Start downloading this segment directly instead of using channel
				go r.downloadSegmentAsync(i)
			}
		}
	}()
}

// downloadSegmentAsync downloads a single segment asynchronously.
// This replaces the channel-based approach for on-demand prefetching.
func (r *m3U8Reader) downloadSegmentAsync(index int) {
	// Double-check bounds and closure status
	if index >= len(r.segments) {
		return
	}

	r.closeMu.RLock()
	if r.closed {
		r.closeMu.RUnlock()
		return
	}
	r.closeMu.RUnlock()

	segment := r.segments[index]

	// Check if already downloaded (race condition protection)
	if segment.data != nil {
		return
	}

	data, err := r.downloadSegmentToMemory(segment)
	if err != nil {
		// Log error but don't fail the entire download
		select {
		case r.errorChan <- err:
		default:
		}
		return
	}

	// Store the downloaded data
	r.segments[index].data = data
}

// openSegmentWithRetry opens a segment with retry logic.
func (r *m3U8Reader) openSegmentWithRetry(segment *segmentInfo) (io.ReadCloser, error) {
	var lastErr error
	for attempt := 0; attempt < r.maxRetries; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(attempt) * r.retryDelay)
		}
		reader, err := r.openSegment(segment)
		if err == nil {
			return reader, nil
		}
		lastErr = err
		segment.Retries++
		if isNonRetryableError(err) {
			break
		}
	}
	return nil, fmt.Errorf("failed to open segment after %d attempts: %w", r.maxRetries, lastErr)
}

// openSegment opens and optionally decrypts a segment with zero-copy approach.
func (r *m3U8Reader) openSegment(segment *segmentInfo) (io.ReadCloser, error) {
	tempFile := filepath.Join(r.tempDir, fmt.Sprintf("segment_%d.ts", len(r.cleanup)))
	r.cleanup = append(r.cleanup, tempFile)
	if err := r.downloadSegmentWithRetry(segment.URI, tempFile, segment.Headers); err != nil {
		return nil, fmt.Errorf("failed to download segment: %w", err)
	}
	file, err := os.Open(tempFile)
	if err != nil {
		return nil, fmt.Errorf("failed to open segment file: %w", err)
	}
	if segment.Key != nil && segment.Key.Method == "AES-128" {
		return r.createDecryptedReader(file, segment.Key)
	}
	return file, nil
}

// downloadSegmentWithRetry downloads a segment with retry logic.
func (r *m3U8Reader) downloadSegmentWithRetry(segmentURL, outputPath string, headers http.Header) error {
	var lastErr error
	for attempt := 0; attempt < r.maxRetries; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(attempt) * r.retryDelay)
		}
		err := r.downloadSegment(segmentURL, outputPath, headers)
		if err == nil {
			return nil
		}
		lastErr = err
		if isNonRetryableError(err) {
			break
		}
	}
	return fmt.Errorf("failed to download segment after %d attempts: %w", r.maxRetries, lastErr)
}

// downloadSegment downloads a segment to local file with zero-copy optimization.
func (r *m3U8Reader) downloadSegment(segmentURL, outputPath string, headers http.Header) error {
	req := r.client.R().
		SetDoNotParseResponse(true)
	if headers != nil {
		req.Header = headers.Clone()
	}

	resp, err := req.Get(segmentURL)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.RawBody().Close()
	if resp.StatusCode() != http.StatusOK {
		return fmt.Errorf("HTTP error: %s", resp.Status())
	}
	file, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("failed to create file: %w", err)
	}
	defer file.Close()
	_, err = io.Copy(file, resp.RawBody())
	if err != nil {
		return fmt.Errorf("failed to write segment: %w", err)
	}
	return nil
}

// createDecryptedReader creates a reader that decrypts AES-128 encrypted segments.
func (r *m3U8Reader) createDecryptedReader(file *os.File, key *m3u8.Key) (io.ReadCloser, error) {
	keyData, err := r.downloadKeyWithRetry(key.URI)
	if err != nil {
		file.Close()
		return nil, fmt.Errorf("failed to download encryption key: %w", err)
	}
	block, err := aes.NewCipher(keyData)
	if err != nil {
		file.Close()
		return nil, fmt.Errorf("failed to create AES cipher: %w", err)
	}
	iv := make([]byte, aes.BlockSize)
	if key.IV != "" {
		ivStr := strings.TrimPrefix(key.IV, "0x")
		if len(ivStr) != 32 {
			file.Close()
			return nil, fmt.Errorf("invalid IV length: %d", len(ivStr))
		}
		for i := 0; i < 16; i++ {
			b, err := strconv.ParseUint(ivStr[i*2:(i+1)*2], 16, 8)
			if err != nil {
				file.Close()
				return nil, fmt.Errorf("invalid IV format: %w", err)
			}
			iv[i] = byte(b)
		}
	}
	decryptor := cipher.NewCBCDecrypter(block, iv)
	return &decryptedReader{
		file:      file,
		decryptor: decryptor,
		buffer:    make([]byte, aes.BlockSize),
	}, nil
}

// downloadKeyWithRetry downloads the encryption key with retry logic.
func (r *m3U8Reader) downloadKeyWithRetry(keyURL string) ([]byte, error) {
	var lastErr error
	for attempt := 0; attempt < r.maxRetries; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(attempt) * r.retryDelay)
		}
		keyData, err := r.downloadKey(keyURL)
		if err == nil {
			return keyData, nil
		}
		lastErr = err
		if isNonRetryableError(err) {
			break
		}
	}
	return nil, fmt.Errorf("failed to download key after %d attempts: %w", r.maxRetries, lastErr)
}

// downloadKey downloads the encryption key for AES decryption.
func (r *m3U8Reader) downloadKey(keyURL string) ([]byte, error) {
	req := r.client.R().
		SetDoNotParseResponse(true)

	resp, err := req.Get(keyURL)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.RawBody().Close()
	if resp.StatusCode() != http.StatusOK {
		return nil, fmt.Errorf("HTTP error downloading key: %s", resp.Status())
	}
	keyData, err := io.ReadAll(resp.RawBody())
	if err != nil {
		return nil, fmt.Errorf("failed to read key data: %w", err)
	}
	if len(keyData) != 16 {
		return nil, fmt.Errorf("invalid key length: expected 16 bytes, got %d", len(keyData))
	}
	return keyData, nil
}

// Close cleans up resources and temporary files.
// Enhanced to properly signal shutdown to all goroutines.
func (r *m3U8Reader) Close() error {
	r.closeMu.Lock()
	if r.closed {
		r.closeMu.Unlock()
		return nil
	}
	r.closed = true
	r.closeMu.Unlock()

	r.mu.Lock()
	defer r.mu.Unlock()

	var lastErr error
	if r.currentReader != nil {
		if err := r.currentReader.Close(); err != nil {
			lastErr = err
		}
		r.currentReader = nil
	}

	// Signal shutdown to all workers and prefetch goroutines
	select {
	case r.errorChan <- fmt.Errorf("reader closed"):
	default:
	}

	// Clean up temporary files
	for _, file := range r.cleanup {
		if err := os.Remove(file); err != nil && !os.IsNotExist(err) {
			lastErr = err
		}
	}
	if r.tempDir != "" {
		if err := os.RemoveAll(r.tempDir); err != nil && !os.IsNotExist(err) {
			lastErr = err
		}
	}
	return lastErr
}

// decryptedReader wraps a file reader with AES-CBC decryption.
type decryptedReader struct {
	file      *os.File
	decryptor cipher.BlockMode
	buffer    []byte
	remainder []byte
}

// Read decrypts data on-the-fly using AES-CBC.
func (dr *decryptedReader) Read(p []byte) (n int, err error) {
	if len(dr.remainder) > 0 {
		n = copy(p, dr.remainder)
		dr.remainder = dr.remainder[n:]
		if len(dr.remainder) == 0 {
			dr.remainder = nil
		}
		return n, nil
	}
	blockBuf := make([]byte, ((len(p)/aes.BlockSize)+1)*aes.BlockSize)
	readBytes, err := dr.file.Read(blockBuf)
	if err != nil && err != io.EOF {
		return 0, err
	}
	if readBytes == 0 {
		return 0, io.EOF
	}
	completeBlocks := (readBytes / aes.BlockSize) * aes.BlockSize
	if completeBlocks > 0 {
		decrypted := make([]byte, completeBlocks)
		for i := 0; i < completeBlocks; i += aes.BlockSize {
			dr.decryptor.CryptBlocks(decrypted[i:i+aes.BlockSize], blockBuf[i:i+aes.BlockSize])
		}
		if err == io.EOF && completeBlocks == readBytes {
			decrypted = removePKCS7Padding(decrypted)
		}
		n = copy(p, decrypted)
		if n < len(decrypted) {
			dr.remainder = decrypted[n:]
		}
	}
	return n, err
}

// Close closes the underlying file.
func (dr *decryptedReader) Close() error {
	return dr.file.Close()
}

// removePKCS7Padding removes PKCS7 padding from decrypted data.
func removePKCS7Padding(data []byte) []byte {
	if len(data) == 0 {
		return data
	}
	padLen := int(data[len(data)-1])
	if padLen > len(data) || padLen > aes.BlockSize {
		return data
	}
	for i := len(data) - padLen; i < len(data); i++ {
		if data[i] != byte(padLen) {
			return data
		}
	}
	return data[:len(data)-padLen]
}
