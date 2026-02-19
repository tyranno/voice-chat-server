package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

type YouTubeResult struct {
	VideoID   string `json:"videoId"`
	Title     string `json:"title"`
	Thumbnail string `json:"thumbnail"`
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
