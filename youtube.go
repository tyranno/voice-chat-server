package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"
)

// liveHLSCache caches the yt-dlp resolved HLS URL so manifest refreshes don't call yt-dlp every 5s.
type liveHLSEntry struct {
	hlsURL  string
	expires time.Time
}

// streamInfoCache caches full stream info (URL, title, duration, isLive) to avoid double yt-dlp calls.
type streamInfoEntry struct {
	info    *StreamInfo
	expires time.Time
}

var (
	liveHLSCacheMu    sync.Mutex
	liveHLSCache      = make(map[string]liveHLSEntry)
	liveResolving     sync.Map // videoID → *sync.Mutex (prevents concurrent yt-dlp for same video)
	streamInfoCacheMu sync.Mutex
	streamInfoCache   = make(map[string]streamInfoEntry)
)

func getCachedStreamInfo(videoID string) (*StreamInfo, bool) {
	streamInfoCacheMu.Lock()
	defer streamInfoCacheMu.Unlock()
	entry, ok := streamInfoCache[videoID]
	if !ok || time.Now().After(entry.expires) {
		return nil, false
	}
	return entry.info, true
}

func setCachedStreamInfo(videoID string, info *StreamInfo) {
	streamInfoCacheMu.Lock()
	defer streamInfoCacheMu.Unlock()
	streamInfoCache[videoID] = streamInfoEntry{info: info, expires: time.Now().Add(5 * time.Hour)}
}

func getCachedHLSURL(videoID string) (string, bool) {
	liveHLSCacheMu.Lock()
	defer liveHLSCacheMu.Unlock()
	entry, ok := liveHLSCache[videoID]
	if !ok || time.Now().After(entry.expires) {
		return "", false
	}
	return entry.hlsURL, true
}

func setCachedHLSURL(videoID, hlsURL string, ttl time.Duration) {
	liveHLSCacheMu.Lock()
	defer liveHLSCacheMu.Unlock()
	liveHLSCache[videoID] = liveHLSEntry{
		hlsURL:  hlsURL,
		expires: time.Now().Add(ttl),
	}
}

type YouTubeResult struct {
	VideoID   string `json:"videoId"`
	Title     string `json:"title"`
	Thumbnail string `json:"thumbnail"`
}

type StreamInfo struct {
	AudioURL string `json:"audioUrl"`
	Title    string `json:"title"`
	Duration int    `json:"duration"`
	IsLive   bool   `json:"isLive"`
}

func (api *APIServer) handleYouTubeStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "Method not allowed", 405)
		return
	}
	videoID := r.URL.Query().Get("videoId")
	if videoID == "" {
		http.Error(w, "Missing videoId parameter", 400)
		return
	}

	// Check stream info cache first (populated by previous /stream or /proxy calls)
	info, cached := getCachedStreamInfo(videoID)
	if !cached {
		var err error
		info, err = resolveYouTubeStream(videoID)
		if err != nil {
			log.Printf("[YouTube] Stream resolve error for %s: %v", videoID, err)
			http.Error(w, fmt.Sprintf("Stream resolve failed: %v", err), 500)
			return
		}
		setCachedStreamInfo(videoID, info)
	}

	// Pre-warm the HLS proxy cache for live streams so the first manifest fetch is fast.
	if info.IsLive {
		setCachedHLSURL(videoID, info.AudioURL, 5*time.Hour)
		log.Printf("[YouTube] Pre-warmed HLS cache for live stream %s (cached=%v)", videoID, cached)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(info)
}

// handleYouTubeProxy relays YouTube audio through this server to avoid client IP-bound URL issues.
func (api *APIServer) handleYouTubeProxy(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "Method not allowed", 405)
		return
	}
	videoID := r.URL.Query().Get("videoId")
	if videoID == "" {
		http.Error(w, "Missing videoId parameter", 400)
		return
	}

	// Check cache first (may be pre-populated by /api/youtube/stream call)
	info, cached := getCachedStreamInfo(videoID)
	if !cached {
		var err error
		info, err = resolveYouTubeStream(videoID)
		if err != nil {
			log.Printf("[YouTube] Proxy resolve error for %s: %v", videoID, err)
			http.Error(w, fmt.Sprintf("Stream resolve failed: %v", err), 500)
			return
		}
		setCachedStreamInfo(videoID, info)
	}
	log.Printf("[YouTube] Proxy for %s (cached=%v, isLive=%v)", videoID, cached, info.IsLive)

	req, err := http.NewRequest("GET", info.AudioURL, nil)
	if err != nil {
		http.Error(w, "Failed to create upstream request", 500)
		return
	}
	if rg := r.Header.Get("Range"); rg != "" {
		req.Header.Set("Range", rg)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")

	client := &http.Client{Timeout: 0}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("[YouTube] Proxy upstream error for %s: %v", videoID, err)
		http.Error(w, "Upstream fetch failed", 502)
		return
	}
	defer resp.Body.Close()

	for _, h := range []string{"Content-Type", "Content-Length", "Content-Range", "Accept-Ranges", "Cache-Control"} {
		if v := resp.Header.Get(h); v != "" {
			w.Header().Set(h, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	if _, err := io.Copy(w, resp.Body); err != nil {
		log.Printf("[YouTube] Proxy copy error for %s: %v", videoID, err)
	}
}

// handleYouTubeHLSProxy serves a rewritten HLS manifest that routes all segments through this server.
// Required for live streams: YouTube HLS segment URLs are IP-bound to the server that resolved them.
func (api *APIServer) handleYouTubeHLSProxy(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "Method not allowed", 405)
		return
	}
	videoID := r.URL.Query().Get("videoId")
	if videoID == "" {
		http.Error(w, "Missing videoId parameter", 400)
		return
	}

	// Check cache first — manifest refreshes happen every 5s, yt-dlp takes 10-30s.
	// Use per-video mutex to prevent concurrent yt-dlp calls for the same video.
	hlsURL, cached := getCachedHLSURL(videoID)
	if !cached {
		// Get or create a per-video mutex
		mu := &sync.Mutex{}
		actual, _ := liveResolving.LoadOrStore(videoID, mu)
		videoMu := actual.(*sync.Mutex)

		videoMu.Lock()
		defer videoMu.Unlock()

		// Re-check cache after acquiring lock (another goroutine may have resolved it)
		hlsURL, cached = getCachedHLSURL(videoID)
		if !cached {
			var err error
			hlsURL, err = resolveLiveHLSURL(videoID)
			if err != nil {
				log.Printf("[HLSProxy] Failed to resolve HLS for %s: %v", videoID, err)
				http.Error(w, fmt.Sprintf("HLS resolve failed: %v", err), 500)
				return
			}
			setCachedHLSURL(videoID, hlsURL, 5*time.Hour)
			log.Printf("[HLSProxy] yt-dlp resolved and cached HLS URL for %s", videoID)
		} else {
			log.Printf("[HLSProxy] Using cache after lock for %s", videoID)
		}
	} else {
		log.Printf("[HLSProxy] Using cached HLS URL for %s", videoID)
	}

	// Fetch the manifest from YouTube CDN using the server's authorized IP
	manifest, fetchErr := fetchRemoteText(hlsURL)
	if fetchErr != nil {
		log.Printf("[HLSProxy] Failed to fetch manifest for %s: %v", videoID, fetchErr)
		http.Error(w, fmt.Sprintf("Manifest fetch failed: %v", fetchErr), 500)
		return
	}

	// Trim to live edge (last 6 segments) + rewrite segment URLs through our proxy.
	// Long-running live streams accumulate thousands of past segments → multi-MB manifests.
	scheme := "https"
	if r.TLS == nil {
		scheme = "http"
	}
	baseURL := fmt.Sprintf("%s://%s", scheme, r.Host)
	rewritten := trimAndRewriteHLSManifest(manifest, baseURL, 6)

	log.Printf("[HLSProxy] Serving trimmed+rewritten HLS manifest for %s (orig=%d bytes, trimmed=%d bytes)",
		videoID, len(manifest), len(rewritten))
	w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	w.Header().Set("Cache-Control", "no-cache, no-store")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.WriteHeader(200)
	fmt.Fprint(w, rewritten)
}

// handleYouTubeHLSSegment proxies individual HLS segments/sub-manifests from YouTube CDN.
func (api *APIServer) handleYouTubeHLSSegment(w http.ResponseWriter, r *http.Request) {
	// r.URL.Query().Get() already URL-decodes once — that's enough.
	// A second QueryUnescape would corrupt YouTube's %25-encoded signature characters.
	segURL := r.URL.Query().Get("url")
	if segURL == "" {
		http.Error(w, "Missing url parameter", 400)
		return
	}

	// Validate it's a YouTube CDN URL
	if !strings.Contains(segURL, "googlevideo.com") && !strings.Contains(segURL, "youtube.com") {
		http.Error(w, "Only YouTube CDN URLs are supported", 403)
		return
	}

	req, err := http.NewRequest("GET", segURL, nil)
	if err != nil {
		http.Error(w, "Invalid segment URL", 400)
		return
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")
	if rg := r.Header.Get("Range"); rg != "" {
		req.Header.Set("Range", rg)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		preview := segURL
	if len(preview) > 60 {
		preview = preview[:60]
	}
	log.Printf("[HLSSegment] Fetch error for %s: %v", preview, err)
		http.Error(w, "Segment fetch failed", 502)
		return
	}
	defer resp.Body.Close()

	// Check if this is a sub-manifest (m3u8) — use Content-Type only (not URL, since YouTube
	// segment URLs may contain "/playlist/index.m3u8/" as a path component even for TS segments).
	contentType := resp.Header.Get("Content-Type")
	isManifest := strings.Contains(contentType, "mpegurl")
	if isManifest {
		body, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			http.Error(w, "Read failed", 500)
			return
		}
		scheme := "https"
		if r.TLS == nil {
			scheme = "http"
		}
		baseURL := fmt.Sprintf("%s://%s", scheme, r.Host)
		rewritten := rewriteHLSManifest(string(body), baseURL)
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		w.Header().Set("Cache-Control", "no-cache, no-store")
		fmt.Fprint(w, rewritten)
		return
	}

	for _, h := range []string{"Content-Type", "Content-Length", "Content-Range", "Accept-Ranges"} {
		if v := resp.Header.Get(h); v != "" {
			w.Header().Set(h, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

// resolveLiveHLSURL extracts the HLS manifest URL for a YouTube video/live stream via yt-dlp.
// Tries multiple formats to handle both VOD and live streams.
func resolveLiveHLSURL(videoID string) (string, error) {
	ytURL := fmt.Sprintf("https://www.youtube.com/watch?v=%s", videoID)
	formats := []string{"bestaudio", "bestaudio/best", "91", "93", "best"}
	var lastErr error

	for _, format := range formats {
		cmd := exec.Command("yt-dlp",
			"--print", "%(url)s",
			"--format", format,
			"--no-playlist",
			"--no-warnings",
			"--no-check-certificates",
			"--geo-bypass",
			"--js-runtimes", "node:/usr/bin/node",
			ytURL,
		)
		var stdout, stderr strings.Builder
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			lastErr = fmt.Errorf("format=%s: %s", format, strings.TrimSpace(stderr.String()))
			log.Printf("[HLSProxy] yt-dlp format=%s failed: %v", format, lastErr)
			continue
		}
		hlsURL := strings.TrimSpace(stdout.String())
		if hlsURL != "" {
			log.Printf("[HLSProxy] Resolved URL for %s via format=%s", videoID, format)
			return hlsURL, nil
		}
	}
	return "", fmt.Errorf("all formats failed: %v", lastErr)
}

// fetchRemoteText fetches a remote URL and returns body as string.
func fetchRemoteText(remoteURL string) (string, error) {
	req, err := http.NewRequest("GET", remoteURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// trimAndRewriteHLSManifest trims a live HLS manifest to the last N segments and rewrites URLs.
// Long-running live streams (e.g. 24h Lofi radio) accumulate thousands of past segments,
// resulting in multi-MB manifests that overwhelm ExoPlayer. We keep only the live edge.
func trimAndRewriteHLSManifest(manifest, baseURL string, keepSegments int) string {
	lines := strings.Split(manifest, "\n")

	// Separate header lines from segment lines
	var headerLines []string
	type segmentPair struct{ meta, urlLine string }
	var segments []segmentPair

	mediaSeqBase := int64(0)
	inHeader := true

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		// Parse #EXT-X-MEDIA-SEQUENCE to track segment numbering
		if strings.HasPrefix(trimmed, "#EXT-X-MEDIA-SEQUENCE:") {
			fmt.Sscanf(trimmed, "#EXT-X-MEDIA-SEQUENCE:%d", &mediaSeqBase)
			inHeader = true
			headerLines = append(headerLines, line)
			continue
		}

		// Segment metadata line (e.g. #EXTINF, #EXT-X-PROGRAM-DATE-TIME, #EXT-X-DISCONTINUITY)
		isSegmentMeta := strings.HasPrefix(trimmed, "#EXTINF") ||
			strings.HasPrefix(trimmed, "#EXT-X-PROGRAM-DATE-TIME") ||
			strings.HasPrefix(trimmed, "#EXT-X-DISCONTINUITY")

		if isSegmentMeta {
			inHeader = false
			segments = append(segments, segmentPair{meta: line})
			continue
		}

		// URL line (follows a segment meta)
		if !inHeader && (strings.HasPrefix(trimmed, "http://") || strings.HasPrefix(trimmed, "https://")) {
			if len(segments) > 0 && segments[len(segments)-1].urlLine == "" {
				segments[len(segments)-1].urlLine = line
			} else {
				segments = append(segments, segmentPair{urlLine: line})
			}
			continue
		}

		if inHeader {
			headerLines = append(headerLines, line)
		}
	}

	// Keep only the last N segments (live edge)
	if len(segments) > keepSegments {
		dropped := len(segments) - keepSegments
		mediaSeqBase += int64(dropped)
		segments = segments[dropped:]
	}

	// Rebuild with updated sequence number and rewritten URLs
	var result []string
	for _, h := range headerLines {
		if strings.HasPrefix(strings.TrimSpace(h), "#EXT-X-MEDIA-SEQUENCE:") {
			result = append(result, fmt.Sprintf("#EXT-X-MEDIA-SEQUENCE:%d", mediaSeqBase))
		} else {
			result = append(result, h)
		}
	}
	for _, seg := range segments {
		if seg.meta != "" {
			result = append(result, seg.meta)
		}
		if seg.urlLine != "" {
			rawURL := strings.TrimSpace(seg.urlLine)
			encoded := url.QueryEscape(rawURL)
			result = append(result, baseURL+"/api/youtube/hls-segment?url="+encoded)
		}
	}

	return strings.Join(result, "\n") + "\n"
}

// rewriteHLSManifest rewrites absolute URLs in an HLS manifest to pass through the server proxy.
// Used for sub-manifests (fetched via hls-segment) which are typically small.
func rewriteHLSManifest(manifest, baseURL string) string {
	lines := strings.Split(manifest, "\n")
	result := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "http://") || strings.HasPrefix(trimmed, "https://") {
			encoded := url.QueryEscape(trimmed)
			result = append(result, baseURL+"/api/youtube/hls-segment?url="+encoded)
		} else {
			result = append(result, line)
		}
	}
	return strings.Join(result, "\n")
}

// resolveYouTubeStream uses yt-dlp to extract a direct audio URL for a YouTube video or live stream.
func resolveYouTubeStream(videoID string) (*StreamInfo, error) {
	ytURL := fmt.Sprintf("https://www.youtube.com/watch?v=%s", videoID)

	// Live streams need different format codes (91=48kbps HLS, 93=128kbps HLS)
	// Try bestaudio first; fall back to live-specific formats, then best
	formats := []string{"bestaudio", "bestaudio/best", "93", "91", "best"}
	var lastErr error

	for _, format := range formats {
		cmd := exec.Command("yt-dlp",
			"--print", "%(url)s\t%(title)s\t%(duration)s\t%(is_live)s",
			"--format", format,
			"--no-playlist",
			"--no-warnings",
			"--no-check-certificates",
			"--geo-bypass",
			"--js-runtimes", "node:/usr/bin/node",
			ytURL,
		)

		var stdout, stderr strings.Builder
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr

		if err := cmd.Run(); err != nil {
			errMsg := strings.TrimSpace(stderr.String())
			if errMsg == "" {
				errMsg = err.Error()
			}
			lastErr = fmt.Errorf("%s", errMsg)
			log.Printf("[YouTube] yt-dlp format=%s failed for %s: %s", format, videoID, errMsg)
			continue
		}

		parts := strings.SplitN(strings.TrimSpace(stdout.String()), "\t", 4)
		if len(parts) < 1 || parts[0] == "" {
			lastErr = fmt.Errorf("yt-dlp returned empty output")
			continue
		}

		audioURL := parts[0]
		title := videoID
		duration := 0
		isLive := false

		if len(parts) >= 2 && parts[1] != "" {
			title = parts[1]
		}
		if len(parts) >= 3 {
			fmt.Sscanf(parts[2], "%d", &duration)
		}
		if len(parts) >= 4 {
			isLiveStr := strings.TrimSpace(parts[3])
			isLive = isLiveStr == "True" || isLiveStr == "true"
		}
		// Also detect live by URL pattern (HLS manifest from googlevideo)
		if strings.Contains(audioURL, "manifest.googlevideo.com") || strings.Contains(audioURL, ".m3u8") {
			isLive = true
		}

		preview := audioURL
		if len(preview) > 60 {
			preview = preview[:60]
		}
		log.Printf("[YouTube] yt-dlp resolved (format=%s, isLive=%v) for %s: %s...", format, isLive, videoID, preview)
		return &StreamInfo{AudioURL: audioURL, Title: title, Duration: duration, IsLive: isLive}, nil
	}

	return nil, fmt.Errorf("yt-dlp failed: %v", lastErr)
}

func (api *APIServer) handleYouTubeSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "Method not allowed", 405)
		return
	}

	query := r.URL.Query().Get("q")
	if query == "" {
		http.Error(w, "Missing q parameter", 400)
		return
	}

	results, err := searchYouTube(query)
	if err != nil {
		log.Printf("[YouTube] Search error: %v", err)
		http.Error(w, fmt.Sprintf("Search failed: %v", err), 500)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(results)
}

func searchYouTube(query string) ([]YouTubeResult, error) {
	searchURL := fmt.Sprintf("https://www.youtube.com/results?search_query=%s", url.QueryEscape(query))

	client := &http.Client{Timeout: 10 * time.Second}
	req, _ := http.NewRequest("GET", searchURL, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	req.Header.Set("Accept-Language", "ko-KR,ko;q=0.9,en;q=0.8")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read failed: %w", err)
	}

	html := string(body)
	return parseYouTubeResults(html)
}

func parseYouTubeResults(html string) ([]YouTubeResult, error) {
	// Extract ytInitialData JSON from the HTML
	re := regexp.MustCompile(`var ytInitialData\s*=\s*(\{.+?\});\s*</script>`)
	match := re.FindStringSubmatch(html)
	if match == nil {
		re2 := regexp.MustCompile(`ytInitialData\s*=\s*'(\{.+?\})'`)
		match = re2.FindStringSubmatch(html)
	}
	if match == nil {
		return nil, fmt.Errorf("could not find ytInitialData")
	}

	jsonStr := match[1]

	// Split by "videoRenderer" and extract videoId + title from each chunk
	var results []YouTubeResult
	seen := make(map[string]bool)

	chunks := strings.Split(jsonStr, `"videoRenderer":{`)
	for i := 1; i < len(chunks); i++ {
		chunk := chunks[i]
		// Extract videoId
		vidRe := regexp.MustCompile(`"videoId":"([a-zA-Z0-9_-]{11})"`)
		vidMatch := vidRe.FindStringSubmatch(chunk)
		if vidMatch == nil {
			continue
		}
		vid := vidMatch[1]
		if seen[vid] {
			continue
		}
		seen[vid] = true

		// Extract title - look for "title":{"runs":[{"text":"..."}]}
		title := "YouTube Video"
		titleRe := regexp.MustCompile(`"title":\{"runs":\[\{"text":"((?:[^"\\]|\\.)*)"\}`)
		titleMatch := titleRe.FindStringSubmatch(chunk)
		if titleMatch != nil {
			title = unescapeJSON(titleMatch[1])
		}

		results = append(results, YouTubeResult{
			VideoID:   vid,
			Title:     title,
			Thumbnail: fmt.Sprintf("https://i.ytimg.com/vi/%s/mqdefault.jpg", vid),
		})

		if len(results) >= 20 {
			break
		}
	}

	if len(results) == 0 {
		return nil, fmt.Errorf("no results found")
	}

	log.Printf("[YouTube] Found %d results for query", len(results))
	return results, nil
}

func unescapeJSON(s string) string {
	s = strings.ReplaceAll(s, `\"`, `"`)
	s = strings.ReplaceAll(s, `\\`, `\`)
	s = strings.ReplaceAll(s, `\u0026`, "&")
	s = strings.ReplaceAll(s, `\u003c`, "<")
	s = strings.ReplaceAll(s, `\u003e`, ">")
	s = strings.ReplaceAll(s, `\u0027`, "'")
	return s
}
