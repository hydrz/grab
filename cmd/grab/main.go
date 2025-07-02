package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"

	"github.com/schollz/progressbar/v3"
	"github.com/spf13/cobra"

	"github.com/hydrz/grab"
	_ "github.com/hydrz/grab/extractors"
	"github.com/hydrz/grab/version"
)

var option grab.Option

func init() {
	// Set default values for options
	option = *grab.DefaultOptions
}

// ProgressManager manages multiple progress bars
type ProgressManager struct {
	bars map[string]*progressbar.ProgressBar
	mu   sync.RWMutex
}

func NewProgressManager() *ProgressManager {
	return &ProgressManager{
		bars: make(map[string]*progressbar.ProgressBar),
	}
}

func (pm *ProgressManager) createProgressCallback() grab.ProgressCallback {
	return func(current, total int64, description string) {
		pm.mu.Lock()
		defer pm.mu.Unlock()

		bar, exists := pm.bars[description]
		if !exists {
			bar = progressbar.DefaultBytes(total, description)
			pm.bars[description] = bar
		}
		bar.Set64(current)
	}
}

func (pm *ProgressManager) finish() {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	for _, bar := range pm.bars {
		bar.Finish()
	}
	pm.bars = make(map[string]*progressbar.ProgressBar)
}

// createRootCommand creates the main command.
func createRootCommand() *cobra.Command {
	var headerFlags []string
	cmd := &cobra.Command{
		Use:     "grab [URL...]",
		Short:   "A versatile media downloader",
		Long:    `grab - Download videos, audios and other media from various sites`,
		Version: version.Version,
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := processHeaders(headerFlags); err != nil {
				return err
			}
			return runRootCommand(cmd, args)
		},
	}
	setupFlags(cmd, &headerFlags)
	return cmd
}

// runRootCommand executes the grab command with the provided context and URLs.
func runRootCommand(cmd *cobra.Command, urls []string) error {
	ctx := grab.NewContext(cmd.Context(), option)

	// Setup progress manager if not in silent mode
	var progressManager *ProgressManager
	if !ctx.Option().Silent {
		progressManager = NewProgressManager()
		ctx.SetProgressCallback(progressManager.createProgressCallback())
		defer progressManager.finish()
	}

	for _, url := range urls {
		url = strings.TrimSpace(url)
		if url == "" {
			continue
		}
		extractor, err := grab.FindExtractor(ctx, url)
		if err != nil {
			return fmt.Errorf("failed to find extractor for URL %s: %w", url, err)
		}

		medias, err := extractor.Extract(url)
		if err != nil {
			return fmt.Errorf("failed to extract media from URL %s: %w", url, err)
		}

		if ctx.Option().ExtractOnly {
			if len(medias) == 0 {
				fmt.Printf("No media found for URL: %s\n", url)
				continue
			}
			fmt.Println("Media information:")
			for _, media := range medias {
				fmt.Println(media.String())
			}
			return nil
		}

		downloader := grab.NewDownloader(ctx)

		if err := downloader.Download(medias); err != nil {
			return fmt.Errorf("failed to download media for URL %s: %w", url, err)
		}
	}

	return nil
}

// processHeaders parses and validates HTTP headers from command line flags.
func processHeaders(headerFlags []string) error {
	if option.Headers == nil {
		option.Headers = make(http.Header)
	}
	for _, h := range headerFlags {
		parts := strings.SplitN(h, ":", 2)
		if len(parts) != 2 {
			return fmt.Errorf("invalid header format: %s", h)
		}
		option.Headers.Set(strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]))
	}
	return nil
}

// setupFlags configures command line flags using the current values in option as defaults.
func setupFlags(cmd *cobra.Command, headerFlags *[]string) {
	// Output options
	cmd.Flags().StringVarP(&option.OutputPath, "output-dir", "o", option.OutputPath, "Output directory for downloaded files")
	cmd.Flags().StringVarP(&option.OutputName, "output-filename", "O", option.OutputName, "Output filename")
	// Quality and format
	cmd.Flags().StringVarP(&option.Quality, "quality", "q", option.Quality, "Preferred video quality")
	cmd.Flags().StringVarP(&option.Format, "format", "f", option.Format, "Output format")
	// Network options
	cmd.Flags().StringVarP(&option.Cookie, "cookies", "c", option.Cookie, "Cookie file path")
	cmd.Flags().StringArrayVarP(headerFlags, "header", "H", nil, "Custom HTTP headers")
	cmd.Flags().StringVarP(&option.UserAgent, "user-agent", "u", option.UserAgent, "Custom user agent")
	cmd.Flags().StringVarP(&option.Proxy, "proxy", "x", option.Proxy, "HTTP proxy URL")
	cmd.Flags().IntVarP(&option.RetryCount, "retry", "r", option.RetryCount, "Number of retry attempts")
	cmd.Flags().DurationVarP(&option.Timeout, "timeout", "t", option.Timeout, "Request timeout")
	cmd.Flags().BoolVar(&option.NoCache, "no-cache", option.NoCache, "Disable HTTP caching")
	// Download options
	cmd.Flags().IntVarP(&option.Threads, "threads", "n", option.Threads, "Number of concurrent download threads")
	cmd.Flags().Int64Var(&option.ChunkSize, "chunk-size", option.ChunkSize, "Download chunk size in bytes")
	cmd.Flags().BoolVarP(&option.SkipExisting, "skip", "s", option.SkipExisting, "Skip download if file already exists")
	// Behavior options
	cmd.Flags().BoolVarP(&option.ExtractOnly, "info", "i", option.ExtractOnly, "Only extract media info, do not download")
	cmd.Flags().BoolVarP(&option.Playlist, "playlist", "p", option.Playlist, "Download all videos in playlist")
	cmd.Flags().IntVar(&option.PlaylistStart, "playlist-start", option.PlaylistStart, "Playlist start index")
	cmd.Flags().IntVar(&option.PlaylistEnd, "playlist-end", option.PlaylistEnd, "Playlist end index")
	// Content options
	cmd.Flags().BoolVar(&option.Subtitle, "subtitle", option.Subtitle, "Download subtitles")
	cmd.Flags().BoolVar(&option.VideoOnly, "video-only", option.VideoOnly, "Download video only, no audio")
	cmd.Flags().BoolVar(&option.AudioOnly, "audio-only", option.AudioOnly, "Download audio only")
	// Error handling and logging
	cmd.Flags().BoolVar(&option.IgnoreErrors, "ignore-errors", option.IgnoreErrors, "Continue on errors")
	cmd.Flags().BoolVarP(&option.Debug, "debug", "d", option.Debug, "Enable debug logging")
	cmd.Flags().BoolVarP(&option.Verbose, "verbose", "v", option.Verbose, "Enable verbose output")
	cmd.Flags().BoolVar(&option.Silent, "silent", option.Silent, "Suppress all output except errors")
}

func main() {
	// Handle graceful shutdown
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	rootCmd := createRootCommand()
	if err := rootCmd.ExecuteContext(ctx); err != nil {
		os.Exit(1)
	}
}
