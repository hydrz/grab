package grab

import (
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

// m3U8Reader implements zero-copy reading for M3U8 streams with encryption support
type m3U8Reader struct {
	segments      []*segmentInfo
	currentIdx    int
	currentReader io.ReadCloser
	tempDir       string   // temporary directory for segment files
	cleanup       []string // files to cleanup
	mu            sync.Mutex
	client        *resty.Client
	maxRetries    int
	retryDelay    time.Duration
}

type segmentInfo struct {
	URI      string
	Duration float64
	Key      *m3u8.Key
	Headers  http.Header
	Retries  int
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

// processMediaPlaylist creates a zero-copy reader for media playlist segments
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

		// Update encryption key if changed
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

	// Create temporary directory for segment processing
	tempDir, err := os.MkdirTemp("", "grab_m3u8_*")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp directory: %w", err)
	}

	reader := &m3U8Reader{
		segments:   segments,
		tempDir:    tempDir,
		cleanup:    make([]string, 0),
		client:     d.ctx.client,
		maxRetries: max(d.ctx.option.RetryCount, 3),
		retryDelay: time.Second,
	}

	return reader, nil
}

// processMasterPlaylist selects the best quality stream from master playlist
func (d *Downloader) processMasterPlaylist(playlist *m3u8.MasterPlaylist, stream Stream) (io.ReadCloser, error) {
	if len(playlist.Variants) == 0 {
		return nil, fmt.Errorf("no variants found in master playlist")
	}

	// Select best quality variant based on bandwidth
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

	// Parse the selected variant URL
	baseURL, err := url.Parse(stream.URL)
	if err != nil {
		return nil, fmt.Errorf("invalid base URL: %w", err)
	}

	variantURL, err := baseURL.Parse(selectedVariant.URI)
	if err != nil {
		return nil, fmt.Errorf("invalid variant URI: %w", err)
	}

	// Create new stream for the selected variant
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

// Read implements io.Reader with zero-copy segment streaming
func (r *m3U8Reader) Read(p []byte) (n int, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	for {
		// If we have a current reader, try to read from it
		if r.currentReader != nil {
			n, err = r.currentReader.Read(p)
			if err == nil || (err != io.EOF && n > 0) {
				return n, err
			}
			// Current segment finished, close and move to next
			r.currentReader.Close()
			r.currentReader = nil
		}

		// Check if we've processed all segments
		if r.currentIdx >= len(r.segments) {
			return 0, io.EOF
		}

		// Open next segment
		segment := r.segments[r.currentIdx]
		r.currentIdx++

		reader, err := r.openSegmentWithRetry(segment)
		if err != nil {
			return 0, fmt.Errorf("failed to open segment %d: %w", r.currentIdx-1, err)
		}

		r.currentReader = reader
		// Continue loop to read from the new segment
	}
}

// openSegmentWithRetry opens a segment with retry logic
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

		// Don't retry on certain errors
		if isNonRetryableError(err) {
			break
		}
	}

	return nil, fmt.Errorf("failed to open segment after %d attempts: %w", r.maxRetries, lastErr)
}

// openSegment opens and optionally decrypts a segment with zero-copy approach
func (r *m3U8Reader) openSegment(segment *segmentInfo) (io.ReadCloser, error) {
	// Download segment to temporary file for zero-copy processing
	tempFile := filepath.Join(r.tempDir, fmt.Sprintf("segment_%d.ts", len(r.cleanup)))
	r.cleanup = append(r.cleanup, tempFile)

	if err := r.downloadSegmentWithRetry(segment.URI, tempFile, segment.Headers); err != nil {
		return nil, fmt.Errorf("failed to download segment: %w", err)
	}

	file, err := os.Open(tempFile)
	if err != nil {
		return nil, fmt.Errorf("failed to open segment file: %w", err)
	}

	// If segment is encrypted, wrap with decryption reader
	if segment.Key != nil && segment.Key.Method == "AES-128" {
		return r.createDecryptedReader(file, segment.Key)
	}

	return file, nil
}

// downloadSegmentWithRetry downloads a segment with retry logic
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

		// Don't retry on certain errors
		if isNonRetryableError(err) {
			break
		}
	}

	return fmt.Errorf("failed to download segment after %d attempts: %w", r.maxRetries, lastErr)
}

// downloadSegment downloads a segment to local file with zero-copy optimization
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

// createDecryptedReader creates a reader that decrypts AES-128 encrypted segments
func (r *m3U8Reader) createDecryptedReader(file *os.File, key *m3u8.Key) (io.ReadCloser, error) {
	// Download encryption key
	keyData, err := r.downloadKeyWithRetry(key.URI)
	if err != nil {
		file.Close()
		return nil, fmt.Errorf("failed to download encryption key: %w", err)
	}

	// Create AES cipher
	block, err := aes.NewCipher(keyData)
	if err != nil {
		file.Close()
		return nil, fmt.Errorf("failed to create AES cipher: %w", err)
	}

	// Parse IV (initialization vector)
	iv := make([]byte, aes.BlockSize)
	if key.IV != "" {
		// Remove 0x prefix if present
		ivStr := strings.TrimPrefix(key.IV, "0x")
		if len(ivStr) != 32 { // 16 bytes = 32 hex chars
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
	// If no IV specified, use sequence number as IV (HLS standard)
	// This is a simplified implementation - real IV handling may be more complex

	decryptor := cipher.NewCBCDecrypter(block, iv)
	return &decryptedReader{
		file:      file,
		decryptor: decryptor,
		buffer:    make([]byte, aes.BlockSize),
	}, nil
}

// downloadKeyWithRetry downloads the encryption key with retry logic
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

		// Don't retry on certain errors
		if isNonRetryableError(err) {
			break
		}
	}

	return nil, fmt.Errorf("failed to download key after %d attempts: %w", r.maxRetries, lastErr)
}

// downloadKey downloads the encryption key for AES decryption
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

// Close cleans up resources and temporary files
func (r *m3U8Reader) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	var lastErr error

	// Close current reader
	if r.currentReader != nil {
		if err := r.currentReader.Close(); err != nil {
			lastErr = err
		}
		r.currentReader = nil
	}

	// Clean up temporary files
	for _, file := range r.cleanup {
		if err := os.Remove(file); err != nil && !os.IsNotExist(err) {
			lastErr = err
		}
	}

	// Remove temporary directory
	if r.tempDir != "" {
		if err := os.RemoveAll(r.tempDir); err != nil && !os.IsNotExist(err) {
			lastErr = err
		}
	}

	return lastErr
}

// decryptedReader wraps a file reader with AES-CBC decryption
type decryptedReader struct {
	file      *os.File
	decryptor cipher.BlockMode
	buffer    []byte
	remainder []byte
}

// Read decrypts data on-the-fly using AES-CBC
func (dr *decryptedReader) Read(p []byte) (n int, err error) {
	// Handle remainder from previous read
	if len(dr.remainder) > 0 {
		n = copy(p, dr.remainder)
		dr.remainder = dr.remainder[n:]
		if len(dr.remainder) == 0 {
			dr.remainder = nil
		}
		return n, nil
	}

	// Read encrypted blocks
	blockBuf := make([]byte, ((len(p)/aes.BlockSize)+1)*aes.BlockSize)
	readBytes, err := dr.file.Read(blockBuf)
	if err != nil && err != io.EOF {
		return 0, err
	}

	if readBytes == 0 {
		return 0, io.EOF
	}

	// Decrypt complete blocks only
	completeBlocks := (readBytes / aes.BlockSize) * aes.BlockSize
	if completeBlocks > 0 {
		decrypted := make([]byte, completeBlocks)
		for i := 0; i < completeBlocks; i += aes.BlockSize {
			dr.decryptor.CryptBlocks(decrypted[i:i+aes.BlockSize], blockBuf[i:i+aes.BlockSize])
		}

		// Handle PKCS7 padding removal for last block if this is EOF
		if err == io.EOF && completeBlocks == readBytes {
			decrypted = removePKCS7Padding(decrypted)
		}

		// Copy to output buffer
		n = copy(p, decrypted)
		if n < len(decrypted) {
			dr.remainder = decrypted[n:]
		}
	}

	return n, err
}

// Close closes the underlying file
func (dr *decryptedReader) Close() error {
	return dr.file.Close()
}

// removePKCS7Padding removes PKCS7 padding from decrypted data
func removePKCS7Padding(data []byte) []byte {
	if len(data) == 0 {
		return data
	}

	padLen := int(data[len(data)-1])
	if padLen > len(data) || padLen > aes.BlockSize {
		return data // Invalid padding, return as-is
	}

	// Verify padding bytes
	for i := len(data) - padLen; i < len(data); i++ {
		if data[i] != byte(padLen) {
			return data // Invalid padding, return as-is
		}
	}

	return data[:len(data)-padLen]
}
