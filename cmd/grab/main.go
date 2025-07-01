package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/schollz/progressbar/v3"
	"github.com/spf13/cobra"

	"github.com/hydrz/grab"
	_ "github.com/hydrz/grab/extractors"
	"github.com/hydrz/grab/version"
)

var option grab.Option

func main() {
	// Handle graceful shutdown
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	option.Ctx = ctx

	rootCmd := createRootCommand()
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
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
			downloader := grab.NewDownloader(option)
			return processURLsWithProgress(downloader, args)
		},
	}

	setupFlags(cmd, &headerFlags)
	return cmd
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

// processURLsWithProgress handles downloading from multiple URLs and manages progress bar display.
func processURLsWithProgress(downloader *grab.Downloader, urls []string) error {
	var lastError error
	successCount := 0

	var bar *progressbar.ProgressBar
	downloader.ProgressCallback = func(current, total int) {
		if bar == nil && total > 0 && !option.Silent {
			bar = progressbar.NewOptions(
				total,
				progressbar.OptionSetDescription("downloading"),
				progressbar.OptionSetWriter(os.Stdout),
				progressbar.OptionSetWidth(40),
				progressbar.OptionSetTheme(progressbar.Theme{Saucer: "#", SaucerHead: ">", SaucerPadding: "-", BarStart: "[", BarEnd: "]"}),
				progressbar.OptionSetRenderBlankState(true),
				progressbar.OptionSetPredictTime(true),
				progressbar.OptionSetElapsedTime(true),
				progressbar.OptionShowBytes(true),
			)
		}
		if bar != nil {
			bar.Set(current)
		}
	}

	for i, url := range urls {
		url = strings.TrimSpace(url)
		if url == "" {
			continue
		}

		if !option.Silent {
			fmt.Printf("Processing (%d/%d): %s\n", i+1, len(urls), url)
		}

		bar = nil // Reset progress bar for each URL

		err := downloader.Download(url)
		if bar != nil {
			bar.Finish()
		}
		if err != nil {
			lastError = err
			if option.IgnoreErrors {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				continue
			}
			return err
		}

		successCount++
	}

	if len(urls) > 1 && !option.Silent {
		fmt.Printf("Summary: %d/%d URLs processed successfully\n", successCount, len(urls))
	}

	if successCount == 0 && lastError != nil {
		return lastError
	}

	return nil
}
