package main

import (
	"bytes"
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

	// proxySemaphore limits concurrent YouTube audio proxy connections to avoid memory/goroutine buildup.
	// 32 slots: client now uses 16-Range parallel download (max throughput for long files), so we need
	// 16 + 1 streaming + headroom for stale connections (broken-pipe slots take ~2 min to release)
	// + room for additional users. Previous: 16 was getting saturated when one user downloaded a
	// long file (4 hours+) at full parallelism.
	proxySemaphore = make(chan struct{}, 32)
)

// startCacheGC starts a background goroutine that periodically evicts expired cache entries
// to prevent unbounded memory growth from stale YouTube stream info and HLS URL entries.
func startCacheGC() {
	go func() {
		ticker := time.NewTicker(30 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			now := time.Now()
			var streamEvicted, hlsEvicted int

			// Evict expired streamInfoCache entries
			streamInfoCacheMu.Lock()
			for k, v := range streamInfoCache {
				if now.After(v.expires) {
					delete(streamInfoCache, k)
					streamEvicted++
				}
			}
			streamInfoCacheMu.Unlock()

			// Evict expired liveHLSCache entries
			liveHLSCacheMu.Lock()
			for k, v := range liveHLSCache {
				if now.After(v.expires) {
					delete(liveHLSCache, k)
					hlsEvicted++
				}
			}
			liveHLSCacheMu.Unlock()

			// Clear all liveResolving entries (per-video mutexes; safe to reset between GC cycles)
			var resolvingEvicted int
			liveResolving.Range(func(k, _ interface{}) bool {
				liveResolving.Delete(k)
				resolvingEvicted++
				return true
			})

			log.Printf("[CacheGC] Evicted streamInfo=%d, hlsURL=%d, resolving=%d",
				streamEvicted, hlsEvicted, resolvingEvicted)
		}
	}()
}

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
	streamInfoCache[videoID] = streamInfoEntry{info: info, expires: time.Now().Add(1 * time.Hour)}
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
		setCachedHLSURL(videoID, info.AudioURL, 30*time.Minute)
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

	// Limit concurrent proxy connections to prevent memory/goroutine accumulation.
	select {
	case proxySemaphore <- struct{}{}:
		defer func() { <-proxySemaphore }()
	default:
		log.Printf("[YouTube] Proxy semaphore full, rejecting request for %s", videoID)
		http.Error(w, "Too many concurrent proxy connections, try again later", 503)
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
	clientRange := r.Header.Get("Range")
	log.Printf("[YouTube] Proxy for %s (cached=%v, isLive=%v, clientRange=%q)", videoID, cached, info.IsLive, clientRange)

	req, err := http.NewRequest("GET", info.AudioURL, nil)
	if err != nil {
		http.Error(w, "Failed to create upstream request", 500)
		return
	}
	// CRITICAL: YouTube CDN throttles plain GET to ~30KB/s.
	// Always send a Range header upstream — Range requests bypass the throttle
	// (verified: 31MB/s with Range vs 31KB/s without).
	// If client sent its own Range (e.g. ExoPlayer), forward it. Otherwise force bytes=0-.
	if clientRange != "" {
		req.Header.Set("Range", clientRange)
	} else {
		req.Header.Set("Range", "bytes=0-")
	}
	// Mobile UA helps with some YouTube CDN routing
	req.Header.Set("User-Agent",
		"Mozilla/5.0 (Linux; Android 13) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Mobile Safari/537.36")

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
	// Always preserve 206 from upstream — keeping Content-Range/Content-Length lets
	// nginx and the client size buffers correctly. Normalizing to 200 forces chunked
	// transfer-encoding which serializes badly with proxy_buffering off.
	w.WriteHeader(resp.StatusCode)
	startedAt := time.Now()
	n, err := io.Copy(w, resp.Body)
	if err != nil {
		msg := err.Error()
		if strings.Contains(msg, "broken pipe") || strings.Contains(msg, "connection reset") || strings.Contains(msg, "write: closed") {
			// client disconnected mid-stream — normal for range requests / buffered players
		} else {
			log.Printf("[YouTube] Proxy copy error for %s after %d bytes: %v", videoID, n, err)
		}
	} else {
		elapsed := time.Since(startedAt)
		speed := float64(n) / elapsed.Seconds()
		log.Printf("[YouTube] Proxy done for %s: %d bytes in %.2fs (%.0f KB/s, upstream=%d, clientRange=%q)",
			videoID, n, elapsed.Seconds(), speed/1024, resp.StatusCode, clientRange)
	}
}

// handleYouTubeAudio: mp3 transcode endpoint (requires ffmpeg). Not used currently — m4a
// preference in resolveYouTubeStream gives broad player compatibility without transcoding.
// Kept for future use if ffmpeg is added.
//nolint:unused
func (api *APIServer) handleYouTubeAudio_unused(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "Method not allowed", 405)
		return
	}
	videoID := r.URL.Query().Get("videoId")
	if videoID == "" {
		http.Error(w, "Missing videoId parameter", 400)
		return
	}

	// Limit concurrent transcodes (ffmpeg is CPU-heavy)
	select {
	case proxySemaphore <- struct{}{}:
		defer func() { <-proxySemaphore }()
	default:
		http.Error(w, "Too many concurrent transcodes, try again later", 503)
		return
	}

	// Resolve the source audio URL via cache or yt-dlp
	info, cached := getCachedStreamInfo(videoID)
	if !cached {
		var err error
		info, err = resolveYouTubeStream(videoID)
		if err != nil {
			log.Printf("[Audio] resolve error for %s: %v", videoID, err)
			http.Error(w, fmt.Sprintf("Stream resolve failed: %v", err), 500)
			return
		}
		setCachedStreamInfo(videoID, info)
	}
	if info.IsLive {
		http.Error(w, "Live streams cannot be downloaded as mp3", 400)
		return
	}

	bitrate := r.URL.Query().Get("bitrate")
	if bitrate == "" {
		bitrate = "192k"
	}

	w.Header().Set("Content-Type", "audio/mpeg")
	w.Header().Set("Cache-Control", "no-cache")
	// Hint filename for the client
	safe := sanitizeFilenameStr(info.Title) + ".mp3"
	w.Header().Set("Content-Disposition", "attachment; filename=\""+safe+"\"")
	w.Header().Set("X-Accel-Buffering", "no")

	// ffmpeg: stream the YouTube audio URL, transcode to mp3, write to stdout.
	// Range header trick: pass via -headers so ffmpeg's HTTP client sends it upstream,
	// bypassing YouTube's plain-GET throttle.
	cmd := exec.Command("ffmpeg",
		"-headers", "Range: bytes=0-\r\nUser-Agent: Mozilla/5.0\r\n",
		"-i", info.AudioURL,
		"-vn",
		"-c:a", "libmp3lame",
		"-b:a", bitrate,
		"-f", "mp3",
		"-loglevel", "error",
		"-nostdin",
		"pipe:1",
	)
	cmd.Stdout = w
	var stderr strings.Builder
	cmd.Stderr = &stderr

	startedAt := time.Now()
	log.Printf("[Audio] mp3 transcode start for %s (bitrate=%s)", videoID, bitrate)
	if err := cmd.Run(); err != nil {
		log.Printf("[Audio] ffmpeg failed for %s: %v stderr=%s", videoID, err, stderr.String())
		// Headers may already be sent; just log and let client see truncated stream
		return
	}
	log.Printf("[Audio] mp3 transcode done for %s in %.2fs", videoID, time.Since(startedAt).Seconds())
}

// sanitizeFilenameStr makes a string safe for an HTTP filename header.
func sanitizeFilenameStr(s string) string {
	if s == "" {
		return "track"
	}
	out := make([]rune, 0, len(s))
	for _, r := range s {
		if r < 32 {
			continue
		}
		switch r {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|':
			out = append(out, '_')
		default:
			out = append(out, r)
		}
	}
	if len(out) > 100 {
		out = out[:100]
	}
	return strings.TrimSpace(string(out))
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
			setCachedHLSURL(videoID, hlsURL, 30*time.Minute)
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
		const maxManifestSize = 1 * 1024 * 1024 // 1MB limit for sub-manifests
		lr := io.LimitReader(resp.Body, maxManifestSize+1)
		body, readErr := io.ReadAll(lr)
		if readErr != nil {
			http.Error(w, "Read failed", 500)
			return
		}
		if len(body) > maxManifestSize {
			log.Printf("[HLSSegment] Sub-manifest too large (>1MB), rejecting")
			http.Error(w, "Sub-manifest too large", 500)
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
			// JS runtime: auto-detect from PATH (deno preferred; falls back to node).
		// Previously hardcoded /usr/bin/node which doesn't exist on NanoPi (only nvm Node 16),
		// causing yt-dlp to fall back to no-JS extraction → webm/opus instead of m4a.
		// Auto-detect finds /usr/local/bin/deno on NanoPi and any node/deno on GCP.
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
// Single yt-dlp invocation with combined format fallback chain — yt-dlp itself handles the cascade.
// Saves 4 process startups vs. previous loop (~1-2s each).
func resolveYouTubeStream(videoID string) (*StreamInfo, error) {
	ytURL := fmt.Sprintf("https://www.youtube.com/watch?v=%s", videoID)

	// Prefer m4a (AAC in MP4 container) — universally recognized by Android music players
	// (Samsung Music, Google Play Music, VLC, etc). webm/opus is YouTube's cheaper alternative
	// but many stock players can't decode it.
	// 93/91 are live-stream HLS codes.
	formatChain := "bestaudio[ext=m4a]/bestaudio[ext=mp4]/bestaudio/bestaudio*/93/91/best"

	cmd := exec.Command("yt-dlp",
		"--print", "%(url)s\t%(title)s\t%(duration)s\t%(is_live)s",
		"--format", formatChain,
		"--no-playlist",
		"--no-warnings",
		"--no-check-certificates",
		"--geo-bypass",
		// JS runtime: auto-detect from PATH (deno preferred; falls back to node).
		// Previously hardcoded /usr/bin/node which doesn't exist on NanoPi (only nvm Node 16),
		// causing yt-dlp to fall back to no-JS extraction → webm/opus instead of m4a.
		// Auto-detect finds /usr/local/bin/deno on NanoPi and any node/deno on GCP.
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
		return nil, fmt.Errorf("yt-dlp failed for %s: %s", videoID, errMsg)
	}

	parts := strings.SplitN(strings.TrimSpace(stdout.String()), "\t", 4)
	if len(parts) < 1 || parts[0] == "" {
		return nil, fmt.Errorf("yt-dlp returned empty output for %s", videoID)
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
	log.Printf("[YouTube] yt-dlp resolved (isLive=%v) for %s: %s...", isLive, videoID, preview)
	return &StreamInfo{AudioURL: audioURL, Title: title, Duration: duration, IsLive: isLive}, nil
}

// prewarmSemaphore limits concurrent background yt-dlp prewarms triggered by search.
var prewarmSemaphore = make(chan struct{}, 2)

// prewarmTopResults resolves yt-dlp stream info for top N search results in the background
// so user's first click hits warm cache instead of cold yt-dlp call (10-25s).
func prewarmTopResults(results []YouTubeResult, n int) {
	if n > len(results) {
		n = len(results)
	}
	for i := 0; i < n; i++ {
		videoID := results[i].VideoID
		// Skip if already cached
		if _, ok := getCachedStreamInfo(videoID); ok {
			continue
		}
		go func(vid string) {
			select {
			case prewarmSemaphore <- struct{}{}:
				defer func() { <-prewarmSemaphore }()
			default:
				return // queue full, skip prewarm for this video
			}
			info, err := resolveYouTubeStream(vid)
			if err != nil {
				log.Printf("[Prewarm] Failed for %s: %v", vid, err)
				return
			}
			setCachedStreamInfo(vid, info)
			log.Printf("[Prewarm] Cached %s (isLive=%v)", vid, info.IsLive)
		}(videoID)
	}
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

	// Pagination: offset (default 0) + limit (default 50, max 50).
	offset := 0
	limit := 50
	if v := r.URL.Query().Get("offset"); v != "" {
		fmt.Sscanf(v, "%d", &offset)
		if offset < 0 {
			offset = 0
		}
	}
	if v := r.URL.Query().Get("limit"); v != "" {
		fmt.Sscanf(v, "%d", &limit)
		if limit <= 0 || limit > 50 {
			limit = 50
		}
	}

	// For offset > 0, fetch a bigger pool so we have results to page through.
	// First page (offset=0) returns 50 quickly; later pages need bigger underlying fetch.
	needed := offset + limit
	results, err := searchYouTubeWithSize(query, needed)
	if err != nil {
		log.Printf("[YouTube] Search error: %v", err)
		http.Error(w, fmt.Sprintf("Search failed: %v", err), 500)
		return
	}

	// Slice for the requested window
	totalFetched := len(results)
	if offset >= totalFetched {
		results = []YouTubeResult{}
	} else {
		end := offset + limit
		if end > totalFetched {
			end = totalFetched
		}
		results = results[offset:end]
	}
	log.Printf("[YouTube] Search %q offset=%d limit=%d returned=%d total=%d", query, offset, limit, len(results), totalFetched)

	// Prewarm top 5 results in background so first click hits cache (only on first page)
	if offset == 0 && len(results) > 0 {
		go prewarmTopResults(results, 5)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(results)
}

// Search result cache — same query within 1 hour returns instantly.
var (
	searchCacheMu sync.Mutex
	searchCache   = make(map[string]searchCacheEntry)
)

type searchCacheEntry struct {
	results []YouTubeResult
	expires time.Time
}

func searchYouTube(query string) ([]YouTubeResult, error) {
	return searchYouTubeWithSize(query, 50)
}

// searchYouTubeWithSize fetches up to `size` results. Cache is keyed by query and stores
// the LARGEST fetch so far — subsequent pages don't re-run yt-dlp if cache already has enough.
func searchYouTubeWithSize(query string, size int) ([]YouTubeResult, error) {
	if size <= 0 {
		size = 50
	}
	if size > 300 {
		size = 300
	}

	// Cache check — if cached has >= requested size, return slice.
	searchCacheMu.Lock()
	if entry, ok := searchCache[query]; ok && time.Now().Before(entry.expires) && len(entry.results) >= size {
		searchCacheMu.Unlock()
		return entry.results, nil
	}
	searchCacheMu.Unlock()

	// Fetch fresh — yt-dlp ytsearchN: for paginated results.
	if results, err := searchYouTubeViaYtDlp(query, size); err == nil && len(results) > 0 {
		searchCacheMu.Lock()
		// Only overwrite cache if new result set is larger
		if existing, ok := searchCache[query]; !ok || len(results) > len(existing.results) {
			searchCache[query] = searchCacheEntry{results: results, expires: time.Now().Add(1 * time.Hour)}
		}
		searchCacheMu.Unlock()
		return results, nil
	} else {
		log.Printf("[YouTube] yt-dlp search failed for %q size=%d, falling back to web scrape: %v", query, size, err)
	}

	// Fallback: web scrape (~20 results, very fast)
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
	results, err := parseYouTubeResults(string(body))
	if err != nil {
		return nil, err
	}
	searchCacheMu.Lock()
	searchCache[query] = searchCacheEntry{results: results, expires: time.Now().Add(30 * time.Minute)}
	searchCacheMu.Unlock()
	return results, nil
}

// searchYouTubeViaYtDlp uses yt-dlp's built-in ytsearchN: scheme to fetch up to N results.
// Output: "videoId\ttitle\n" per line.
func searchYouTubeViaYtDlp(query string, n int) ([]YouTubeResult, error) {
	cmd := exec.Command("yt-dlp",
		fmt.Sprintf("ytsearch%d:%s", n, query),
		"--print", "%(id)s\t%(title)s",
		"--no-warnings",
		"--quiet",
		"--flat-playlist",
		"--no-download",
	)
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("yt-dlp: %v: %s", err, errBuf.String())
	}
	var results []YouTubeResult
	seen := make(map[string]bool)
	for _, line := range strings.Split(out.String(), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) != 2 {
			continue
		}
		vid := strings.TrimSpace(parts[0])
		title := strings.TrimSpace(parts[1])
		if len(vid) != 11 || seen[vid] {
			continue
		}
		seen[vid] = true
		results = append(results, YouTubeResult{
			VideoID:   vid,
			Title:     title,
			Thumbnail: fmt.Sprintf("https://i.ytimg.com/vi/%s/mqdefault.jpg", vid),
		})
	}
	return results, nil
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
