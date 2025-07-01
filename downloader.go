package grab

type Downloader interface {
	Name() string
	Download(stream Stream) error
}

type genericDownloader struct{}

type m3u8Downloader struct{}
