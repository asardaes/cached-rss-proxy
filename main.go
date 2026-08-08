package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
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

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		handleRequest(w, r, upstream)
	})

	server := &http.Server{
		Addr:    ":" + port,
		Handler: mux,
	}

	// 1. Listen for termination signals
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	// 2. Start HTTP server in goroutine
	go func() {
		log.Printf("Starting RSS proxy on :%s to upstream %s\n", port, upstream)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("HTTP server error: %v", err)
		}
	}()

	// 3. Block until SIGINT or SIGTERM is received
	sig := <-stop
	log.Printf("Received signal %v. Initiating graceful shutdown...\n", sig)

	// 4. Allow in-flight requests up to 5s to finish writing known.txt
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Fatalf("Server forced to shutdown: %v", err)
	}

	log.Println("Server stopped cleanly.")
}

func handleRequest(w http.ResponseWriter, r *http.Request, upstream string) {
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

	now := time.Now().Unix()
	cache := loadCache(now)
	filteredBody, anyAdded := filterRSS(body, cache, now)
	saveCache(cache, now, anyAdded)

	w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
	w.WriteHeader(resp.StatusCode)
	w.Write(filteredBody)
}

func loadCache(now int64) map[string]int64 {
	cache := make(map[string]int64)
	data, err := os.ReadFile(cacheFile)
	if err != nil {
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

		if epoch >= cutoff {
			cache[parts[1]] = epoch
		}
	}
	return cache
}

func saveCache(cache map[string]int64, now int64, anyAdded bool) {
	var sb strings.Builder
	var anyRemoved bool
	cutoff := now - maxAgeSec

	for link, epoch := range cache {
		if epoch >= cutoff {
			sb.WriteString(fmt.Sprintf("%d\t%s\n", epoch, link))
		} else {
			anyRemoved = true
		}
	}

	if anyAdded || anyRemoved {
		_ = os.WriteFile(cacheFile, []byte(sb.String()), 0644)
	}
}

func filterRSS(feed []byte, cache map[string]int64, now int64) ([]byte, bool) {
	var result []byte
	var anyAdded bool
	rest := feed

	for {
		startIdx := bytes.Index(rest, []byte("<item>"))
		if startIdx == -1 {
			result = append(result, rest...)
			break
		}

		result = append(result, rest[:startIdx]...)
		rest = rest[startIdx:]

		endIdx := bytes.Index(rest, []byte("</item>"))
		if endIdx == -1 {
			result = append(result, rest...)
			break
		}

		itemBytes := rest[:endIdx+7]
		rest = rest[endIdx+7:]

		linkStart := bytes.Index(itemBytes, []byte("<link>"))
		linkEnd := bytes.Index(itemBytes, []byte("</link>"))

		if linkStart != -1 && linkEnd != -1 && linkStart < linkEnd {
			link := string(bytes.TrimSpace(itemBytes[linkStart+6 : linkEnd]))

			if _, exists := cache[link]; !exists {
				cache[link] = now
				result = append(result, itemBytes...)
				anyAdded = true
			}
		} else {
			result = append(result, itemBytes...)
		}
	}

	return result, anyAdded
}
