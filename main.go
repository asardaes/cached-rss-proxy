package main

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	cacheFile = "known.txt"
	// 30 days in seconds
	maxAgeSec = 30 * 24 * 3600
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	upstream := os.Getenv("UPSTREAM_URL")
	if upstream == "" {
		log.Fatal("UPSTREAM_URL environment variable is required")
	}

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		handleRequest(w, r, upstream)
	})

	log.Printf("Starting RSS proxy on :%s to upstream %s\n", port, upstream)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}

func handleRequest(w http.ResponseWriter, r *http.Request, upstream string) {
	// 1. Fetch from the real upstream
	resp, err := http.Get(upstream)
	if err != nil {
		http.Error(w, "Failed to fetch upstream", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		http.Error(w, "Failed to read upstream body", http.StatusInternalServerError)
		return
	}

	// 2. Load the current cache from known.txt
	now := time.Now().Unix()
	cache := loadCache(now)

	// 3. Filter the RSS feed (this also adds new items to our cache map)
	filteredBody := filterRSS(body, cache, now)

	// 4. Overwrite known.txt with the updated cache
	saveCache(cache, now)

	// 5. Respond to the caller
	w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
	w.WriteHeader(resp.StatusCode)
	w.Write(filteredBody)
}

func loadCache(now int64) map[string]int64 {
	cache := make(map[string]int64)
	data, err := os.ReadFile(cacheFile)
	if err != nil {
		// File might not exist yet, return empty cache
		return cache
	}

	cutoff := now - maxAgeSec
	lines := strings.Split(string(data), "\n")

	for _, line := range lines {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) != 2 {
			continue
		}

		epoch, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil {
			continue
		}

		// Only retain entries younger than a month
		if epoch >= cutoff {
			cache[parts[1]] = epoch
		}
	}
	return cache
}

func saveCache(cache map[string]int64, now int64) {
	var sb strings.Builder
	cutoff := now - maxAgeSec

	for link, epoch := range cache {
		if epoch >= cutoff {
			sb.WriteString(fmt.Sprintf("%d\t%s\n", epoch, link))
		}
	}

	// Overwrite the file on each call
	_ = os.WriteFile(cacheFile, []byte(sb.String()), 0644)
}

// filterRSS byte-scans the XML to flawlessly preserve all namespaces and formatting,
// selectively dropping only the <item>...</item> blocks that are known in the cache.
func filterRSS(feed []byte, cache map[string]int64, now int64) []byte {
	var result []byte
	rest := feed

	for {
		startIdx := bytes.Index(rest, []byte("<item>"))
		if startIdx == -1 {
			// No more items, append whatever is left and stop
			result = append(result, rest...)
			break
		}

		// Append the content leading up to the <item> tag
		result = append(result, rest[:startIdx]...)
		rest = rest[startIdx:]

		endIdx := bytes.Index(rest, []byte("</item>"))
		if endIdx == -1 {
			// Malformed XML (missing closing tag), append the rest and stop
			result = append(result, rest...)
			break
		}

		// Extract the full <item>...</item> block
		itemBytes := rest[:endIdx+7]
		rest = rest[endIdx+7:]

		linkStart := bytes.Index(itemBytes, []byte("<link>"))
		linkEnd := bytes.Index(itemBytes, []byte("</link>"))

		if linkStart != -1 && linkEnd != -1 && linkStart < linkEnd {
			// Extract just the link URL
			link := string(bytes.TrimSpace(itemBytes[linkStart+6 : linkEnd]))

			if _, exists := cache[link]; !exists {
				// The item is NEW. Add to cache and keep the item block.
				cache[link] = now
				result = append(result, itemBytes...)
			}
			// If it DOES exist, we simply do nothing (effectively dropping it).
		} else {
			// If we couldn't find a <link>, play it safe and keep the item
			result = append(result, itemBytes...)
		}
	}

	return result
}
