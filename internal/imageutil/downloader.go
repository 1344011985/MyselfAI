package imageutil

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Downloader downloads QQ image attachments to a local cache directory.
type Downloader struct {
	cacheDir string
	maxBytes int64
	client   *http.Client
}

// New creates a Downloader. cacheDir must be non-empty to enable image support.
func New(cacheDir string, maxSizeMB int) (*Downloader, error) {
	if cacheDir == "" {
		return nil, nil // image support disabled
	}
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return nil, fmt.Errorf("create image cache dir %q: %w", cacheDir, err)
	}
	return &Downloader{
		cacheDir: cacheDir,
		maxBytes: int64(maxSizeMB) * 1024 * 1024,
		client:   &http.Client{Timeout: 30 * time.Second},
	}, nil
}

// Download fetches the image at url and saves it to cacheDir.
// Returns the local file path, or an error if download fails or size exceeds limit.
func (d *Downloader) Download(url string) (string, error) {
	resp, err := d.client.Get(url)
	if err != nil {
		return "", fmt.Errorf("fetch image: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("image server returned %d", resp.StatusCode)
	}

	// Determine extension from Content-Type
	ext := extFromContentType(resp.Header.Get("Content-Type"))

	// Unique filename based on timestamp + url hash
	name := fmt.Sprintf("%d%s", time.Now().UnixNano(), ext)
	path := filepath.Join(d.cacheDir, name)

	f, err := os.Create(path)
	if err != nil {
		return "", fmt.Errorf("create cache file: %w", err)
	}
	defer f.Close()

	// Limit read to maxBytes
	limited := io.LimitReader(resp.Body, d.maxBytes+1)
	n, err := io.Copy(f, limited)
	if err != nil {
		os.Remove(path)
		return "", fmt.Errorf("write image: %w", err)
	}
	if n > d.maxBytes {
		os.Remove(path)
		return "", fmt.Errorf("image exceeds size limit (%d MB)", d.maxBytes/1024/1024)
	}

	return path, nil
}

func extFromContentType(ct string) string {
	ct = strings.ToLower(strings.Split(ct, ";")[0])
	switch ct {
	case "image/jpeg", "image/jpg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	default:
		return ".jpg"
	}
}
