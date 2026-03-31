package export

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	maxFileSize  = 2 << 30  // 2 GiB per file
	maxTotalSize = 5 << 30  // 5 GiB total extracted
	maxFileCount = 100_000  // max entries in ZIP
)

// Unzip extracts a ZIP file to destDir and returns the path to result.json.
// It handles both flat ZIPs (result.json at root) and nested (subfolder/result.json).
func Unzip(zipPath, destDir string) (string, error) {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return "", fmt.Errorf("open zip %s: %w", zipPath, err)
	}
	defer r.Close()

	if len(r.File) > maxFileCount {
		return "", fmt.Errorf("zip contains too many files: %d (max %d)", len(r.File), maxFileCount)
	}

	var resultJSON string
	var totalExtracted int64

	for _, f := range r.File {
		// Security: prevent zip slip (path traversal)
		target := filepath.Join(destDir, f.Name)
		if !strings.HasPrefix(filepath.Clean(target), filepath.Clean(destDir)+string(os.PathSeparator)) {
			return "", fmt.Errorf("illegal file path in zip: %s", f.Name)
		}

		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0755); err != nil {
				return "", fmt.Errorf("create dir %s: %w", target, err)
			}
			continue
		}

		// Create parent dirs
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return "", fmt.Errorf("create parent dir for %s: %w", target, err)
		}

		// Extract file
		n, err := extractFile(f, target)
		if err != nil {
			return "", err
		}
		totalExtracted += n
		if totalExtracted > maxTotalSize {
			return "", fmt.Errorf("zip extraction exceeded total size limit (%d bytes)", maxTotalSize)
		}

		// Track result.json
		if filepath.Base(f.Name) == "result.json" {
			resultJSON = target
		}
	}

	if resultJSON == "" {
		return "", fmt.Errorf("result.json not found in zip archive")
	}

	return resultJSON, nil
}

func extractFile(f *zip.File, target string) (int64, error) {
	rc, err := f.Open()
	if err != nil {
		return 0, fmt.Errorf("open zip entry %s: %w", f.Name, err)
	}
	defer rc.Close()

	out, err := os.Create(target)
	if err != nil {
		return 0, fmt.Errorf("create file %s: %w", target, err)
	}
	defer out.Close()

	lr := &io.LimitedReader{R: rc, N: maxFileSize + 1}
	n, err := io.Copy(out, lr)
	if err != nil {
		return n, fmt.Errorf("extract %s: %w", f.Name, err)
	}
	if lr.N == 0 {
		return n, fmt.Errorf("file %s exceeds size limit (%d bytes)", f.Name, maxFileSize)
	}

	return n, nil
}

// ExportInfo holds summary info about a Telegram export.
type ExportInfo struct {
	ChatName     string
	Messages     int
	Photos       int
	Videos       int
	Documents    int
	Other        int
	FirstDate    string // "2 янв 2020"
	LastDate     string // "15 мар 2026"
	MissingMedia int    // media files referenced but not found on disk
}

var ruMonths = [12]string{"янв", "фев", "мар", "апр", "май", "июн", "июл", "авг", "сен", "окт", "ноя", "дек"}

func formatDate(unixStr string) string {
	unix, err := strconv.ParseInt(unixStr, 10, 64)
	if err != nil || unix == 0 {
		return ""
	}
	t := time.Unix(unix, 0)
	return fmt.Sprintf("%d %s %d", t.Day(), ruMonths[t.Month()-1], t.Year())
}

// analyzeMessage is a minimal message shape used only for streaming Analyze.
type analyzeMessage struct {
	Type         string `json:"type"`
	DateUnixtime string `json:"date_unixtime"`
	Photo        string `json:"photo"`
	File         string `json:"file"`
	MediaType    string `json:"media_type"`
}

// Analyze reads result.json and returns export statistics using streaming JSON
// decoding to avoid loading the entire file into memory.
func Analyze(resultJSONPath string) (ExportInfo, error) {
	f, err := os.Open(resultJSONPath)
	if err != nil {
		return ExportInfo{}, fmt.Errorf("read %s: %w", resultJSONPath, err)
	}
	defer f.Close()

	dec := json.NewDecoder(f)

	// Expect opening '{' of the root object.
	if _, err := dec.Token(); err != nil {
		return ExportInfo{}, fmt.Errorf("parse json: %w", err)
	}

	baseDir := filepath.Dir(resultJSONPath)
	var info ExportInfo
	var firstDate, lastDate string

	// Scan top-level keys.
	for dec.More() {
		key, err := dec.Token()
		if err != nil {
			return ExportInfo{}, fmt.Errorf("parse json: %w", err)
		}

		switch key.(string) {
		case "name":
			if err := dec.Decode(&info.ChatName); err != nil {
				return ExportInfo{}, fmt.Errorf("parse json: %w", err)
			}

		case "messages":
			// Expect opening '['.
			if _, err := dec.Token(); err != nil {
				return ExportInfo{}, fmt.Errorf("parse json: expected messages array: %w", err)
			}
			for dec.More() {
				var m analyzeMessage
				if err := dec.Decode(&m); err != nil {
					return ExportInfo{}, fmt.Errorf("parse json: %w", err)
				}
				if m.Type != "message" {
					continue
				}
				info.Messages++

				if firstDate == "" && m.DateUnixtime != "" {
					firstDate = m.DateUnixtime
				}
				if m.DateUnixtime != "" {
					lastDate = m.DateUnixtime
				}

				mediaPath := ""
				switch {
				case m.Photo != "":
					info.Photos++
					mediaPath = m.Photo
				case m.MediaType == "video_file" || m.MediaType == "video_message":
					info.Videos++
					mediaPath = m.File
				case m.File != "" && m.MediaType == "":
					info.Documents++
					mediaPath = m.File
				case m.File != "":
					info.Other++
					mediaPath = m.File
				}

				if mediaPath != "" && !strings.Contains(mediaPath, "(File not included") {
					fullPath := filepath.Join(baseDir, mediaPath)
					if _, statErr := os.Stat(fullPath); os.IsNotExist(statErr) {
						info.MissingMedia++
					}
				}
			}
			// Consume closing ']' so the outer loop can continue reading sibling keys.
			if _, err := dec.Token(); err != nil {
				return ExportInfo{}, fmt.Errorf("parse json: %w", err)
			}

		default:
			// Skip unknown top-level fields.
			var discard json.RawMessage
			if err := dec.Decode(&discard); err != nil {
				return ExportInfo{}, fmt.Errorf("parse json: %w", err)
			}
		}
	}

	info.FirstDate = formatDate(firstDate)
	info.LastDate = formatDate(lastDate)

	return info, nil
}
