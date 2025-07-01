package grab

import (
	"crypto/aes"
	"crypto/cipher"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/go-resty/resty/v2"
	"github.com/grafov/m3u8"
)

// M3U8Downloader handles M3U8 playlist downloads.
type M3U8Downloader struct {
	option Option
	client *resty.Client
	logger *slog.Logger
	// ProgressCallback is called after each segment is downloaded.
	ProgressCallback func(current, total int)
}

// NewM3U8Downloader creates a new M3U8Downloader using Option's resty.Client.
func NewM3U8Downloader(client *resty.Client, option Option) *M3U8Downloader {
	return &M3U8Downloader{
		option: option,
		client: client,
		logger: option.Logger(),
	}
}

// Download downloads an M3U8 playlist and merges segments.
func (d *M3U8Downloader) Download(playlistURL, outputPath string) error {
	playlist, listType, err := d.parsePlaylist(playlistURL)
	if err != nil {
		return fmt.Errorf("failed to parse playlist: %w", err)
	}

	switch listType {
	case m3u8.MEDIA:
		return d.downloadMedia(playlist.(*m3u8.MediaPlaylist), playlistURL, outputPath)
	case m3u8.MASTER:
		return d.downloadMaster(playlist.(*m3u8.MasterPlaylist), playlistURL, outputPath)
	default:
		return fmt.Errorf("unsupported playlist type")
	}
}

// parsePlaylist parses an M3U8 playlist from URL using resty.Client.
func (d *M3U8Downloader) parsePlaylist(playlistURL string) (m3u8.Playlist, m3u8.ListType, error) {
	resp, err := d.client.R().SetDoNotParseResponse(true).Get(playlistURL)
	if err != nil {
		return nil, 0, err
	}
	defer resp.RawBody().Close()

	if resp.StatusCode() != 200 {
		return nil, 0, fmt.Errorf("HTTP error: %s, URL: %s", resp.Status(), playlistURL)
	}

	playlist, listType, err := m3u8.DecodeFrom(resp.RawBody(), true)
	if err != nil {
		return nil, 0, err
	}

	return playlist, listType, nil
}

// downloadMaster downloads from a master playlist.
func (d *M3U8Downloader) downloadMaster(master *m3u8.MasterPlaylist, baseURL, outputPath string) error {
	if len(master.Variants) == 0 {
		return fmt.Errorf("no variants found in master playlist")
	}

	variant := d.selectBestVariant(master.Variants)
	if variant == nil {
		return fmt.Errorf("no suitable variant found")
	}

	variantURL, err := resolveURL(baseURL, variant.URI)
	if err != nil {
		return fmt.Errorf("failed to resolve variant URL: %w", err)
	}

	d.logger.Debug("Selected variant", "bandwidth", variant.Bandwidth, "resolution", variant.Resolution, "url", variantURL)
	return d.Download(variantURL, outputPath)
}

// downloadMedia downloads from a media playlist. Progress is reported via ProgressCallback.
func (d *M3U8Downloader) downloadMedia(media *m3u8.MediaPlaylist, baseURL, outputPath string) error {
	if media.Count() == 0 {
		return fmt.Errorf("no segments found in media playlist")
	}

	d.logger.Debug("Downloading M3U8 stream", "segments", media.Count(), "output", outputPath)

	tempDir := filepath.Join(filepath.Dir(outputPath), ".tmp_"+filepath.Base(outputPath))
	if err := os.MkdirAll(tempDir, 0755); err != nil {
		return fmt.Errorf("failed to create temp directory: %w", err)
	}
	defer os.RemoveAll(tempDir)

	var key []byte
	if media.Key != nil && media.Key.URI != "" {
		keyURL, err := resolveURL(baseURL, media.Key.URI)
		if err != nil {
			return fmt.Errorf("failed to resolve key URL: %w", err)
		}
		key, err = d.downloadKey(keyURL)
		if err != nil {
			return fmt.Errorf("failed to download encryption key: %w", err)
		}
		d.logger.Debug("Downloaded encryption key", "url", keyURL)
	}

	segmentPaths := make([]string, 0, media.Count())
	total := int(media.Count())
	current := 0

	for i, segment := range media.Segments {
		if segment == nil {
			break
		}
		segmentURL, err := resolveURL(baseURL, segment.URI)
		if err != nil {
			d.logger.Error("Failed to resolve segment URL", "index", i, "uri", segment.URI, "error", err)
			current++
			if d.ProgressCallback != nil {
				d.ProgressCallback(current, total)
			}
			continue
		}

		segmentPath := filepath.Join(tempDir, fmt.Sprintf("segment_%04d.ts", i))

		if err := d.downloadSegment(segmentURL, segmentPath, key, segment); err != nil {
			d.logger.Error("Failed to download segment", "index", i, "url", segmentURL, "error", err)
			if !d.option.IgnoreErrors {
				return fmt.Errorf("failed to download segment %d: %w", i, err)
			}
			current++
			if d.ProgressCallback != nil {
				d.ProgressCallback(current, total)
			}
			continue
		}

		segmentPaths = append(segmentPaths, segmentPath)
		current++
		if d.ProgressCallback != nil {
			d.ProgressCallback(current, total)
		}
	}

	if len(segmentPaths) == 0 {
		return fmt.Errorf("no segments downloaded successfully")
	}

	return d.mergeSegments(segmentPaths, outputPath)
}

// downloadSegment downloads a single segment using resty.Client.
func (d *M3U8Downloader) downloadSegment(segmentURL, outputPath string, key []byte, segment *m3u8.MediaSegment) error {
	resp, err := d.client.R().Get(segmentURL)
	if err != nil {
		return err
	}
	defer resp.RawBody().Close()

	if resp.StatusCode() != 200 {
		return fmt.Errorf("HTTP error: %s", resp.Status())
	}

	data, err := io.ReadAll(resp.RawBody())
	if err != nil {
		return err
	}

	if len(key) > 0 {
		data, err = d.decryptSegment(data, key, segment)
		if err != nil {
			return fmt.Errorf("failed to decrypt segment: %w", err)
		}
	}

	return os.WriteFile(outputPath, data, 0644)
}

// downloadKey downloads the encryption key using resty.Client.
func (d *M3U8Downloader) downloadKey(keyURL string) ([]byte, error) {
	resp, err := d.client.R().Get(keyURL)
	if err != nil {
		return nil, err
	}
	defer resp.RawBody().Close()

	if resp.StatusCode() != 200 {
		return nil, fmt.Errorf("HTTP error: %s", resp.Status())
	}

	return io.ReadAll(resp.RawBody())
}

// decryptSegment decrypts an encrypted segment.
func (d *M3U8Downloader) decryptSegment(data, key []byte, segment *m3u8.MediaSegment) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	iv := make([]byte, aes.BlockSize)
	if segment.Key != nil && segment.Key.IV != "" {
		ivStr := strings.TrimPrefix(segment.Key.IV, "0x")
		for i := 0; i < len(ivStr) && i/2 < len(iv); i += 2 {
			b, err := strconv.ParseUint(ivStr[i:i+2], 16, 8)
			if err != nil {
				return nil, fmt.Errorf("invalid IV format: %w", err)
			}
			iv[i/2] = byte(b)
		}
	} else {
		for i := 0; i < 8; i++ {
			iv[aes.BlockSize-1-i] = byte(segment.SeqId >> (i * 8))
		}
	}

	mode := cipher.NewCBCDecrypter(block, iv)

	if len(data)%aes.BlockSize != 0 {
		padding := aes.BlockSize - (len(data) % aes.BlockSize)
		data = append(data, make([]byte, padding)...)
	}

	mode.CryptBlocks(data, data)

	if len(data) > 0 {
		padding := int(data[len(data)-1])
		if padding > 0 && padding <= aes.BlockSize && padding <= len(data) {
			data = data[:len(data)-padding]
		}
	}

	return data, nil
}

// selectBestVariant selects the best quality variant.
func (d *M3U8Downloader) selectBestVariant(variants []*m3u8.Variant) *m3u8.Variant {
	if len(variants) == 0 {
		return nil
	}

	sort.Slice(variants, func(i, j int) bool {
		return variants[i].Bandwidth < variants[j].Bandwidth
	})

	if d.option.Quality != "" && d.option.Quality != "best" && d.option.Quality != "worst" {
		for _, variant := range variants {
			if strings.Contains(variant.Resolution, d.option.Quality) {
				return variant
			}
		}
	}

	if d.option.Quality == "worst" {
		return variants[0]
	}

	return variants[len(variants)-1]
}

// mergeSegments merges segments using ffmpeg.
func (d *M3U8Downloader) mergeSegments(segmentPaths []string, outputPath string) error {
	if len(segmentPaths) == 0 {
		return fmt.Errorf("no segments to merge")
	}

	if err := os.MkdirAll(filepath.Dir(outputPath), 0755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	return concatenateWithFFmpeg(segmentPaths, outputPath)
}

// resolveURL resolves a relative URL against a base URL.
func resolveURL(baseURL, relativeURL string) (string, error) {
	base, err := url.Parse(baseURL)
	if err != nil {
		return "", err
	}

	ref, err := url.Parse(relativeURL)
	if err != nil {
		return "", err
	}

	return base.ResolveReference(ref).String(), nil
}
