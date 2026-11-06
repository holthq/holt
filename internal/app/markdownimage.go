package app

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
)

// 这个文件是 Markdown 预览里图片的读法。本地路径照旧从文档所在的目录读（markdown.FileImages）；
// http / https 的（README 的徽章、GitHub 上的截图）下下来解码，成功过的按地址留着，预览再开不用再下一遍。

// markdownImages 是 Markdown 预览用的 LoadImage。
func markdownImages(dir string) func(string) (ui.ImageSource, error) {
	local := markdown.FileImages(dir)
	return func(u string) (ui.ImageSource, error) {
		if !isRemoteImage(u) {
			return local(u)
		}
		return remoteImage(u)
	}
}

// isRemoteImage 报 url 是要从网上下的一种（http / https）。别的协议（data:、file:……）照旧交给 FileImages。
func isRemoteImage(u string) bool {
	return strings.HasPrefix(u, "https://") || strings.HasPrefix(u, "http://")
}

const (
	imageTimeout = 15 * time.Second // 一张图最多等这么久
	maxImageSize = 8 << 20          // 最多下 8 MB
	maxImages    = 24               // 最多记着几张（按地址，最旧的先丢）
)

var imageClient = &http.Client{Timeout: imageTimeout}

var remoteImages struct {
	sync.Mutex
	order []string
	m     map[string]ui.ImageSource
}

// remoteImage 取一张网上的图：下过、解开过的按地址留着；失败的不留（网断了下次再试）。
func remoteImage(u string) (ui.ImageSource, error) {
	remoteImages.Lock()
	src, ok := remoteImages.m[u]
	remoteImages.Unlock()
	if ok {
		return src, nil
	}
	src, err := downloadImage(u)
	if err != nil {
		return nil, err
	}
	remoteImages.Lock()
	defer remoteImages.Unlock()
	if remoteImages.m == nil {
		remoteImages.m = map[string]ui.ImageSource{}
	}
	if _, ok := remoteImages.m[u]; !ok {
		if len(remoteImages.order) >= maxImages {
			old := remoteImages.order[0]
			remoteImages.order = remoteImages.order[1:]
			delete(remoteImages.m, old)
		}
		remoteImages.order = append(remoteImages.order, u)
		remoteImages.m[u] = src
	}
	return src, nil
}

// downloadImage 下一张图并解开：SVG 看 Content-Type、地址的扩展名和开头几个字节，别的是 PNG / JPEG / GIF / WebP / BMP。
func downloadImage(u string) (ui.ImageSource, error) {
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Holt")
	resp, err := imageClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("图片 %s：HTTP %d", u, resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxImageSize+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxImageSize {
		return nil, fmt.Errorf("图片 %s 超过 %d MB", u, maxImageSize>>20)
	}
	if isSVGImage(resp.Header.Get("Content-Type"), u, data) {
		return ui.ParseSVG(data)
	}
	return ui.DecodeBitmap(data)
}

// isSVGImage 报这份数据是不是 SVG：先看 Content-Type，再看地址的扩展名，最后看开头。
func isSVGImage(contentType, u string, data []byte) bool {
	if strings.Contains(strings.ToLower(contentType), "image/svg") {
		return true
	}
	p := u
	if i := strings.IndexAny(p, "?#"); i >= 0 {
		p = p[:i]
	}
	if strings.HasSuffix(strings.ToLower(p), ".svg") {
		return true
	}
	head := strings.TrimSpace(string(data))
	return strings.HasPrefix(head, "<svg") || strings.HasPrefix(head, "<?xml")
}
