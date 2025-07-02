# grab

## Overview

**grab** is a versatile, extensible media downloader written in Go. It supports downloading videos, audios, documents, and other resources from various platforms, with features like multi-threaded downloads, resumable downloads, playlist support, and customizable extraction.

## Features

- Supports multiple platforms via plugin-like extractors
- Multi-threaded, resumable downloads with chunked HTTP range requests
- M3U8/HLS stream support with zero-copy and AES-128 decryption
- Playlist and batch download support
- Customizable output directory, filename, quality, and format (with ffmpeg integration)
- Progress bars for multiple downloads
- Robust error handling and retry logic
- Custom HTTP headers, cookies, and proxy support
- Download subtitles, video-only, or audio-only as needed
- Extensible: add new extractors easily

## Getting Started

```bash
go install github.com/hydrz/grab/cmd/grab@latest
```

## Usage

```bash
grab [OPTIONS] <URL>...
```

### Common Options

- `-o, --output-dir <dir>`: Output directory (default: ./downloads)
- `-O, --output-filename <name>`: Output filename
- `-q, --quality <quality>`: Preferred quality (e.g., best, 720p)
- `-f, --format <fmt>`: Output format (e.g., mp4, mkv, mp3)
- `-c, --cookies <file>`: Cookie file path
- `-H, --header <header>`: Custom HTTP header (can be used multiple times)
- `-u, --user-agent <ua>`: Custom user agent
- `-x, --proxy <url>`: HTTP proxy URL
- `-r, --retry <n>`: Number of retry attempts
- `-t, --timeout <duration>`: Request timeout (e.g., 30s)
- `-n, --threads <n>`: Number of concurrent download threads
- `--chunk-size <bytes>`: Download chunk size in bytes
- `-S, --no-skip`: Do not skip existing files
- `-i, --info`: Only extract media info, do not download
- `-p, --playlist`: Download all videos in playlist
- `--playlist-start <n>`: Playlist start index
- `--playlist-end <n>`: Playlist end index
- `--subtitle`: Download subtitles
- `--video-only`: Download video only, no audio
- `--audio-only`: Download audio only
- `--ignore-errors`: Continue on errors
- `-d, --debug`: Enable debug logging
- `-v, --verbose`: Enable verbose output
- `--silent`: Suppress all output except errors

### Example

```bash
grab -o ./videos -q best -n 8 "https://example.com/video/123"
```

To see all registered extractors:

```bash
grab --help
```

## Changelog

[![release](https://github.com/hydrz/grab/actions/workflows/release.yml/badge.svg)](https://github.com/hydrz/grab/releases)

## Contributing

We welcome contributions! Please read the [CONTRIBUTING.md](CONTRIBUTING.md) for guidelines on how to contribute to this project.

## License

[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)
This project is licensed under the terms of the MIT license. See the [LICENSE](LICENSE) file for details.
