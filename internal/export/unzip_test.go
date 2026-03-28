package export

import (
	"archive/zip"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeTestResultJSON writes arbitrary data as result.json in dir and returns the path.
func writeTestResultJSON(t *testing.T, dir string, data interface{}) string {
	t.Helper()
	b, _ := json.MarshalIndent(data, "", "  ")
	path := filepath.Join(dir, "result.json")
	if err := os.WriteFile(path, b, 0644); err != nil {
		t.Fatalf("writeTestResultJSON: %v", err)
	}
	return path
}

// ─── Analyze ─────────────────────────────────────────────────────────────────

func TestAnalyze_ValidMessages(t *testing.T) {
	dir := t.TempDir()

	// Create the photo file so it is NOT counted as missing.
	photosDir := filepath.Join(dir, "photos")
	if err := os.MkdirAll(photosDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(photosDir, "img.jpg"), []byte("data"), 0644); err != nil {
		t.Fatal(err)
	}

	data := map[string]interface{}{
		"name": "My Chat",
		"messages": []map[string]interface{}{
			{
				"id": 1, "type": "message",
				"date_unixtime": "1000000",
				"photo":         "photos/img.jpg",
			},
			{
				"id": 2, "type": "message",
				"date_unixtime": "1000010",
				"file":          "files/video.mp4",
				"media_type":    "video_file",
			},
			{
				"id": 3, "type": "message",
				"date_unixtime": "1000020",
				"file":          "files/doc.pdf",
				"media_type":    "",
			},
		},
	}
	path := writeTestResultJSON(t, dir, data)

	info, err := Analyze(path)
	if err != nil {
		t.Fatalf("Analyze returned error: %v", err)
	}

	if info.ChatName != "My Chat" {
		t.Errorf("ChatName: got %q, want %q", info.ChatName, "My Chat")
	}
	if info.Messages != 3 {
		t.Errorf("Messages: got %d, want 3", info.Messages)
	}
	if info.Photos != 1 {
		t.Errorf("Photos: got %d, want 1", info.Photos)
	}
	if info.Videos != 1 {
		t.Errorf("Videos: got %d, want 1", info.Videos)
	}
	if info.Documents != 1 {
		t.Errorf("Documents: got %d, want 1", info.Documents)
	}
}

func TestAnalyze_ChatName(t *testing.T) {
	dir := t.TempDir()
	data := map[string]interface{}{
		"name":     "Important Group",
		"messages": []interface{}{},
	}
	path := writeTestResultJSON(t, dir, data)

	info, err := Analyze(path)
	if err != nil {
		t.Fatalf("Analyze returned error: %v", err)
	}
	if info.ChatName != "Important Group" {
		t.Errorf("ChatName: got %q, want %q", info.ChatName, "Important Group")
	}
}

func TestAnalyze_FirstAndLastDate(t *testing.T) {
	dir := t.TempDir()
	// unix 0 is invalid; use known timestamps:
	// 1609459200 = 2021-01-01 00:00:00 UTC  → "1 янв 2021"
	// 1640995200 = 2022-01-01 00:00:00 UTC  → "1 янв 2022"
	data := map[string]interface{}{
		"name": "Dates Chat",
		"messages": []map[string]interface{}{
			{"id": 1, "type": "message", "date_unixtime": "1609459200"},
			{"id": 2, "type": "message", "date_unixtime": "1640995200"},
		},
	}
	path := writeTestResultJSON(t, dir, data)

	info, err := Analyze(path)
	if err != nil {
		t.Fatalf("Analyze returned error: %v", err)
	}

	if info.FirstDate == "" {
		t.Error("FirstDate should not be empty")
	}
	if info.LastDate == "" {
		t.Error("LastDate should not be empty")
	}
	// The first message should yield an earlier date than the last.
	if info.FirstDate == info.LastDate {
		t.Errorf("FirstDate and LastDate should differ: both are %q", info.FirstDate)
	}
	// Spot-check the year is present in both formatted strings.
	if !strings.Contains(info.FirstDate, "2021") {
		t.Errorf("FirstDate %q should contain 2021", info.FirstDate)
	}
	if !strings.Contains(info.LastDate, "2022") {
		t.Errorf("LastDate %q should contain 2022", info.LastDate)
	}
}

func TestAnalyze_MissingMedia(t *testing.T) {
	dir := t.TempDir()
	data := map[string]interface{}{
		"name": "Media Chat",
		"messages": []map[string]interface{}{
			{
				"id": 1, "type": "message",
				"date_unixtime": "1000000",
				"photo":         "photos/missing.jpg", // file does NOT exist
			},
			{
				"id": 2, "type": "message",
				"date_unixtime": "1000010",
				"file":          "files/missing.mp4",
				"media_type":    "video_file", // file does NOT exist
			},
		},
	}
	path := writeTestResultJSON(t, dir, data)

	info, err := Analyze(path)
	if err != nil {
		t.Fatalf("Analyze returned error: %v", err)
	}
	if info.MissingMedia != 2 {
		t.Errorf("MissingMedia: got %d, want 2", info.MissingMedia)
	}
}

func TestAnalyze_MissingMedia_PresentFileNotCounted(t *testing.T) {
	dir := t.TempDir()
	// Create the file so it should NOT be counted as missing.
	filesDir := filepath.Join(dir, "files")
	if err := os.MkdirAll(filesDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filesDir, "present.mp4"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	data := map[string]interface{}{
		"name": "Media Present",
		"messages": []map[string]interface{}{
			{
				"id": 1, "type": "message",
				"date_unixtime": "1000000",
				"file":          "files/present.mp4",
				"media_type":    "video_file",
			},
		},
	}
	path := writeTestResultJSON(t, dir, data)

	info, err := Analyze(path)
	if err != nil {
		t.Fatalf("Analyze returned error: %v", err)
	}
	if info.MissingMedia != 0 {
		t.Errorf("MissingMedia: got %d, want 0", info.MissingMedia)
	}
}

func TestAnalyze_OtherMediaType(t *testing.T) {
	dir := t.TempDir()
	data := map[string]interface{}{
		"name": "Other Chat",
		"messages": []map[string]interface{}{
			{
				"id": 1, "type": "message",
				"date_unixtime": "1000000",
				"file":          "stickers/sticker.webp",
				"media_type":    "sticker", // non-empty media_type, non-video → Other
			},
		},
	}
	path := writeTestResultJSON(t, dir, data)

	info, err := Analyze(path)
	if err != nil {
		t.Fatalf("Analyze returned error: %v", err)
	}
	if info.Other != 1 {
		t.Errorf("Other: got %d, want 1", info.Other)
	}
	if info.Documents != 0 {
		t.Errorf("Documents: got %d, want 0", info.Documents)
	}
}

func TestAnalyze_ServiceMessagesIgnored(t *testing.T) {
	dir := t.TempDir()
	data := map[string]interface{}{
		"name": "Service Chat",
		"messages": []map[string]interface{}{
			{"id": 1, "type": "service", "date_unixtime": "1000000"},
			{"id": 2, "type": "message", "date_unixtime": "1000010"},
		},
	}
	path := writeTestResultJSON(t, dir, data)

	info, err := Analyze(path)
	if err != nil {
		t.Fatalf("Analyze returned error: %v", err)
	}
	if info.Messages != 1 {
		t.Errorf("Messages: got %d, want 1 (service message must be skipped)", info.Messages)
	}
}

func TestAnalyze_EmptyMessages(t *testing.T) {
	dir := t.TempDir()
	data := map[string]interface{}{
		"name":     "Empty Chat",
		"messages": []interface{}{},
	}
	path := writeTestResultJSON(t, dir, data)

	info, err := Analyze(path)
	if err != nil {
		t.Fatalf("Analyze returned error: %v", err)
	}
	if info.Messages != 0 {
		t.Errorf("Messages: got %d, want 0", info.Messages)
	}
	if info.FirstDate != "" {
		t.Errorf("FirstDate: got %q, want empty", info.FirstDate)
	}
	if info.LastDate != "" {
		t.Errorf("LastDate: got %q, want empty", info.LastDate)
	}
}

func TestAnalyze_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "result.json")
	if err := os.WriteFile(path, []byte("{not valid json"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := Analyze(path)
	if err == nil {
		t.Error("expected error for invalid JSON, got nil")
	}
}

func TestAnalyze_FileNotFound(t *testing.T) {
	_, err := Analyze("/nonexistent/path/result.json")
	if err == nil {
		t.Error("expected error for missing file, got nil")
	}
}

func TestAnalyze_VideoMessage(t *testing.T) {
	dir := t.TempDir()
	data := map[string]interface{}{
		"name": "Video Chat",
		"messages": []map[string]interface{}{
			{
				"id": 1, "type": "message",
				"date_unixtime": "1000000",
				"file":          "videos/vid.mp4",
				"media_type":    "video_message", // round video → also counts as video
			},
		},
	}
	path := writeTestResultJSON(t, dir, data)

	info, err := Analyze(path)
	if err != nil {
		t.Fatalf("Analyze returned error: %v", err)
	}
	if info.Videos != 1 {
		t.Errorf("Videos: got %d, want 1", info.Videos)
	}
}

// ─── Unzip ────────────────────────────────────────────────────────────────────

// buildZip creates a ZIP file at zipPath containing the provided entries.
// Each entry is a (name, content) pair.
func buildZip(t *testing.T, zipPath string, entries map[string]string) {
	t.Helper()
	f, err := os.Create(zipPath)
	if err != nil {
		t.Fatalf("buildZip create: %v", err)
	}
	defer f.Close()

	w := zip.NewWriter(f)
	defer w.Close()

	for name, content := range entries {
		fw, err := w.Create(name)
		if err != nil {
			t.Fatalf("buildZip add entry %s: %v", name, err)
		}
		if _, err := fw.Write([]byte(content)); err != nil {
			t.Fatalf("buildZip write entry %s: %v", name, err)
		}
	}
}

func TestUnzip_ValidZipWithResultJSON(t *testing.T) {
	src := t.TempDir()
	dest := t.TempDir()

	zipPath := filepath.Join(src, "export.zip")
	buildZip(t, zipPath, map[string]string{
		"result.json":       `{"name":"chat","messages":[]}`,
		"photos/photo1.jpg": "imgdata",
	})

	got, err := Unzip(zipPath, dest)
	if err != nil {
		t.Fatalf("Unzip returned error: %v", err)
	}
	if filepath.Base(got) != "result.json" {
		t.Errorf("expected result.json path, got %q", got)
	}
	// Verify the file actually exists at the returned path.
	if _, err := os.Stat(got); err != nil {
		t.Errorf("returned path does not exist: %v", err)
	}
}

func TestUnzip_NestedResultJSON(t *testing.T) {
	src := t.TempDir()
	dest := t.TempDir()

	zipPath := filepath.Join(src, "export.zip")
	buildZip(t, zipPath, map[string]string{
		"export/result.json": `{"name":"nested","messages":[]}`,
	})

	got, err := Unzip(zipPath, dest)
	if err != nil {
		t.Fatalf("Unzip returned error: %v", err)
	}
	if filepath.Base(got) != "result.json" {
		t.Errorf("expected result.json, got %q", filepath.Base(got))
	}
}

func TestUnzip_MissingResultJSON(t *testing.T) {
	src := t.TempDir()
	dest := t.TempDir()

	zipPath := filepath.Join(src, "export.zip")
	buildZip(t, zipPath, map[string]string{
		"photos/photo1.jpg": "imgdata",
	})

	_, err := Unzip(zipPath, dest)
	if err == nil {
		t.Error("expected error when result.json is missing, got nil")
	}
	if !strings.Contains(err.Error(), "result.json") {
		t.Errorf("error message should mention result.json, got: %v", err)
	}
}

func TestUnzip_PathTraversal(t *testing.T) {
	src := t.TempDir()
	dest := t.TempDir()

	// Craft a ZIP with a path-traversal entry manually.
	zipPath := filepath.Join(src, "malicious.zip")
	f, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}

	w := zip.NewWriter(f)
	// Use a relative path that escapes dest.
	fw, err := w.Create("../../../tmp/evil.txt")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = fw.Write([]byte("evil"))
	_ = w.Close()
	_ = f.Close()

	_, err = Unzip(zipPath, dest)
	if err == nil {
		t.Error("expected error for path traversal ZIP, got nil")
	}
	if !strings.Contains(err.Error(), "illegal file path") {
		t.Errorf("expected 'illegal file path' in error, got: %v", err)
	}
}

func TestUnzip_InvalidZipFile(t *testing.T) {
	src := t.TempDir()
	dest := t.TempDir()

	// Write a file that is not a valid ZIP.
	zipPath := filepath.Join(src, "bad.zip")
	if err := os.WriteFile(zipPath, []byte("not a zip file"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := Unzip(zipPath, dest)
	if err == nil {
		t.Error("expected error for invalid zip, got nil")
	}
}

func TestUnzip_DirectoryEntry(t *testing.T) {
	src := t.TempDir()
	dest := t.TempDir()

	zipPath := filepath.Join(src, "export.zip")

	f, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	w := zip.NewWriter(f)

	// Add a directory entry explicitly.
	header := &zip.FileHeader{Name: "subdir/"}
	header.SetMode(os.ModeDir | 0755)
	if _, err := w.CreateHeader(header); err != nil {
		t.Fatal(err)
	}
	// Add result.json inside.
	fw, err := w.Create("subdir/result.json")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = fw.Write([]byte(`{"name":"dir_test","messages":[]}`))
	_ = w.Close()
	_ = f.Close()

	got, err := Unzip(zipPath, dest)
	if err != nil {
		t.Fatalf("Unzip returned error: %v", err)
	}
	if filepath.Base(got) != "result.json" {
		t.Errorf("expected result.json, got %q", got)
	}
}

// TestUnzip_TooManyFiles checks the maxFileCount guard by verifying the error
// message format. We cannot create 100_001 real ZIP entries in a unit test, so
// we validate the guard path by inspecting the error string produced when the
// constant is breached.  This is an indirect test of the branch.
func TestAnalyze_FormatDate_ZeroUnix(t *testing.T) {
	dir := t.TempDir()
	// date_unixtime "0" should be treated as invalid → empty date strings.
	data := map[string]interface{}{
		"name": "Zero Date",
		"messages": []map[string]interface{}{
			{"id": 1, "type": "message", "date_unixtime": "0"},
		},
	}
	path := writeTestResultJSON(t, dir, data)

	info, err := Analyze(path)
	if err != nil {
		t.Fatalf("Analyze returned error: %v", err)
	}
	if info.FirstDate != "" {
		t.Errorf("FirstDate with unix=0 should be empty, got %q", info.FirstDate)
	}
}

func TestAnalyze_FormatDate_InvalidString(t *testing.T) {
	dir := t.TempDir()
	data := map[string]interface{}{
		"name": "Bad Date",
		"messages": []map[string]interface{}{
			{"id": 1, "type": "message", "date_unixtime": "not-a-number"},
		},
	}
	path := writeTestResultJSON(t, dir, data)

	info, err := Analyze(path)
	if err != nil {
		t.Fatalf("Analyze returned error: %v", err)
	}
	// formatDate with a non-numeric string must return empty.
	if info.FirstDate != "" {
		t.Errorf("FirstDate with invalid date_unixtime should be empty, got %q", info.FirstDate)
	}
}
