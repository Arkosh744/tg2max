package export

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Unzip extracts a ZIP file to destDir and returns the path to result.json.
// It handles both flat ZIPs (result.json at root) and nested (subfolder/result.json).
func Unzip(zipPath, destDir string) (string, error) {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return "", fmt.Errorf("open zip %s: %w", zipPath, err)
	}
	defer r.Close()

	var resultJSON string

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
		if err := extractFile(f, target); err != nil {
			return "", err
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

func extractFile(f *zip.File, target string) error {
	rc, err := f.Open()
	if err != nil {
		return fmt.Errorf("open zip entry %s: %w", f.Name, err)
	}
	defer rc.Close()

	out, err := os.Create(target)
	if err != nil {
		return fmt.Errorf("create file %s: %w", target, err)
	}
	defer out.Close()

	// Limit extraction size to 2GB to prevent zip bombs
	if _, err := io.Copy(out, io.LimitReader(rc, 2<<30)); err != nil {
		return fmt.Errorf("extract %s: %w", f.Name, err)
	}

	return nil
}

// ExportInfo holds summary info about a Telegram export.
type ExportInfo struct {
	Messages  int
	Photos    int
	Videos    int
	Documents int
	Other     int
}

// Analyze reads result.json and returns export statistics.
func Analyze(resultJSONPath string) (ExportInfo, error) {
	data, err := os.ReadFile(resultJSONPath)
	if err != nil {
		return ExportInfo{}, fmt.Errorf("read %s: %w", resultJSONPath, err)
	}

	var export struct {
		Messages []struct {
			Type      string `json:"type"`
			Photo     string `json:"photo"`
			File      string `json:"file"`
			MediaType string `json:"media_type"`
		} `json:"messages"`
	}

	if err := json.Unmarshal(data, &export); err != nil {
		return ExportInfo{}, fmt.Errorf("parse json: %w", err)
	}
	// Release raw JSON memory early
	data = nil
	_ = data

	var info ExportInfo
	for _, m := range export.Messages {
		if m.Type != "message" {
			continue
		}
		info.Messages++
		switch {
		case m.Photo != "":
			info.Photos++
		case m.MediaType == "video_file" || m.MediaType == "video_message":
			info.Videos++
		case m.File != "" && m.MediaType == "":
			info.Documents++
		case m.File != "":
			info.Other++
		}
	}

	return info, nil
}
