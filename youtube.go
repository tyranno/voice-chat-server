package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
)

type YouTubeResult struct {
	VideoID   string `json:"videoId"`
	Title     string `json:"title"`
	Thumbnail string `json:"thumbnail"`
}

func (api *APIServer) handleYouTubeSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	query := r.URL.Query().Get("q")
	if query == "" {
		http.Error(w, "Missing query parameter 'q'", http.StatusBadRequest)
		return
	}

	results, err := searchYouTube(query)
	if err != nil {
		log.Printf("[YouTube] Search error: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(results)
}

func searchYouTube(query string) ([]YouTubeResult, error) {
	// Use YouTube's internal API (youtubei)
	apiURL := "https://www.youtube.com/youtubei/v1/search?prettyPrint=false"

	payload := map[string]interface{}{
		"context": map[string]interface{}{
			"client": map[string]interface{}{
				"clientName":    "WEB",
				"clientVersion": "2.20240101.00.00",
				"hl":            "ko",
				"gl":            "KR",
			},
		},
		"query": query,
	}

	body, _ := json.Marshal(payload)
	req, err := http.NewRequest("POST", apiURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Linux; Android 14) AppleWebKit/537.36")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("YouTube API returned %d", resp.StatusCode)
	}

	// Parse the response to extract video results
	var data map[string]interface{}
	if err := json.Unmarshal(respBody, &data); err != nil {
		return nil, fmt.Errorf("JSON parse error: %v", err)
	}

	var results []YouTubeResult
	extractVideos(data, &results)

	if len(results) == 0 {
		return []YouTubeResult{}, nil
	}

	return results, nil
}

func extractVideos(data interface{}, results *[]YouTubeResult) {
	if len(*results) >= 20 {
		return
	}

	switch v := data.(type) {
	case map[string]interface{}:
		// Check if this is a videoRenderer
		if vr, ok := v["videoRenderer"].(map[string]interface{}); ok {
			vid, _ := vr["videoId"].(string)
			if vid != "" {
				title := extractTitle(vr)
				*results = append(*results, YouTubeResult{
					VideoID:   vid,
					Title:     title,
					Thumbnail: fmt.Sprintf("https://i.ytimg.com/vi/%s/mqdefault.jpg", vid),
				})
			}
			return
		}
		for _, val := range v {
			extractVideos(val, results)
		}
	case []interface{}:
		for _, item := range v {
			extractVideos(item, results)
		}
	}
}

func extractTitle(vr map[string]interface{}) string {
	if titleObj, ok := vr["title"].(map[string]interface{}); ok {
		if runs, ok := titleObj["runs"].([]interface{}); ok && len(runs) > 0 {
			if run, ok := runs[0].(map[string]interface{}); ok {
				if text, ok := run["text"].(string); ok {
					return text
				}
			}
		}
	}
	return "제목 없음"
}
