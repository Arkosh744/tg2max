package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeConfig(t *testing.T, dir string, content string) string {
	t.Helper()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write config file: %v", err)
	}
	return path
}

func createExportFile(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "result.json")
	if err := os.WriteFile(path, []byte(`{"messages":[]}`), 0644); err != nil {
		t.Fatalf("create export file: %v", err)
	}
	return path
}

func validYAML(exportPath string) string {
	return `max_token: "test-token-123"
rate_limit_rps: 10
cursor_file: "my_cursor.json"
mappings:
  - name: "chat1"
    tg_export_path: "` + exportPath + `"
    max_chat_id: 42
`
}

func Test_ValidConfig(t *testing.T) {
	dir := t.TempDir()
	exportPath := createExportFile(t, dir)
	cfgPath := writeConfig(t, dir, validYAML(exportPath))

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.MaxToken != "test-token-123" {
		t.Errorf("MaxToken = %q, want %q", cfg.MaxToken, "test-token-123")
	}
	if cfg.RateLimitRPS != 10 {
		t.Errorf("RateLimitRPS = %d, want %d", cfg.RateLimitRPS, 10)
	}
	if cfg.CursorFile != "my_cursor.json" {
		t.Errorf("CursorFile = %q, want %q", cfg.CursorFile, "my_cursor.json")
	}
	if len(cfg.Mappings) != 1 {
		t.Fatalf("len(Mappings) = %d, want 1", len(cfg.Mappings))
	}
	if cfg.Mappings[0].Name != "chat1" {
		t.Errorf("Mappings[0].Name = %q, want %q", cfg.Mappings[0].Name, "chat1")
	}
	if cfg.Mappings[0].TGExportPath != exportPath {
		t.Errorf("Mappings[0].TGExportPath = %q, want %q", cfg.Mappings[0].TGExportPath, exportPath)
	}
	if cfg.Mappings[0].MaxChatID != 42 {
		t.Errorf("Mappings[0].MaxChatID = %d, want %d", cfg.Mappings[0].MaxChatID, 42)
	}
}

func Test_MissingMaxToken(t *testing.T) {
	dir := t.TempDir()
	exportPath := createExportFile(t, dir)

	content := `mappings:
  - name: "chat1"
    tg_export_path: "` + exportPath + `"
    max_chat_id: 42
`
	t.Setenv("MAX_TOKEN", "")
	cfgPath := writeConfig(t, dir, content)

	_, err := Load(cfgPath)
	if err == nil {
		t.Fatal("expected error for missing max_token, got nil")
	}
}

func Test_TokenFromEnv(t *testing.T) {
	dir := t.TempDir()
	exportPath := createExportFile(t, dir)

	content := `mappings:
  - name: "chat1"
    tg_export_path: "` + exportPath + `"
    max_chat_id: 42
`
	t.Setenv("MAX_TOKEN", "env-token-456")
	cfgPath := writeConfig(t, dir, content)

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.MaxToken != "env-token-456" {
		t.Errorf("MaxToken = %q, want %q", cfg.MaxToken, "env-token-456")
	}
}

func Test_NoMappings(t *testing.T) {
	dir := t.TempDir()
	content := `max_token: "some-token"
mappings: []
`
	cfgPath := writeConfig(t, dir, content)

	_, err := Load(cfgPath)
	if err == nil {
		t.Fatal("expected error for empty mappings, got nil")
	}
}

func Test_DefaultRateLimitRPS(t *testing.T) {
	dir := t.TempDir()
	exportPath := createExportFile(t, dir)

	content := `max_token: "some-token"
mappings:
  - name: "chat1"
    tg_export_path: "` + exportPath + `"
    max_chat_id: 42
`
	cfgPath := writeConfig(t, dir, content)

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.RateLimitRPS != 25 {
		t.Errorf("RateLimitRPS = %d, want default 25", cfg.RateLimitRPS)
	}
}

func Test_DefaultCursorFile(t *testing.T) {
	dir := t.TempDir()
	exportPath := createExportFile(t, dir)

	content := `max_token: "some-token"
mappings:
  - name: "chat1"
    tg_export_path: "` + exportPath + `"
    max_chat_id: 42
`
	cfgPath := writeConfig(t, dir, content)

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.CursorFile != "cursor.json" {
		t.Errorf("CursorFile = %q, want default %q", cfg.CursorFile, "cursor.json")
	}
}

func Test_NegativeRateLimitRPS(t *testing.T) {
	dir := t.TempDir()
	exportPath := createExportFile(t, dir)

	content := `max_token: "some-token"
rate_limit_rps: -5
mappings:
  - name: "chat1"
    tg_export_path: "` + exportPath + `"
    max_chat_id: 42
`
	cfgPath := writeConfig(t, dir, content)

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.RateLimitRPS != 25 {
		t.Errorf("RateLimitRPS = %d, want default 25 for negative input", cfg.RateLimitRPS)
	}
}

func Test_EmptyMappingName(t *testing.T) {
	dir := t.TempDir()
	exportPath := createExportFile(t, dir)

	content := `max_token: "some-token"
mappings:
  - name: ""
    tg_export_path: "` + exportPath + `"
    max_chat_id: 42
`
	cfgPath := writeConfig(t, dir, content)

	_, err := Load(cfgPath)
	if err == nil {
		t.Fatal("expected error for empty mapping name, got nil")
	}
}

func Test_DuplicateMappingNames(t *testing.T) {
	dir := t.TempDir()
	exportPath := createExportFile(t, dir)

	content := `max_token: "some-token"
mappings:
  - name: "same"
    tg_export_path: "` + exportPath + `"
    max_chat_id: 1
  - name: "same"
    tg_export_path: "` + exportPath + `"
    max_chat_id: 2
`
	cfgPath := writeConfig(t, dir, content)

	_, err := Load(cfgPath)
	if err == nil {
		t.Fatal("expected error for duplicate mapping names, got nil")
	}
}

func Test_NonExistentExportPath(t *testing.T) {
	dir := t.TempDir()
	content := `max_token: "some-token"
mappings:
  - name: "chat1"
    tg_export_path: "/nonexistent/path/result.json"
    max_chat_id: 42
`
	cfgPath := writeConfig(t, dir, content)

	_, err := Load(cfgPath)
	if err == nil {
		t.Fatal("expected error for nonexistent export path, got nil")
	}
}

func Test_ZeroMaxChatID(t *testing.T) {
	dir := t.TempDir()
	exportPath := createExportFile(t, dir)

	content := `max_token: "some-token"
mappings:
  - name: "chat1"
    tg_export_path: "` + exportPath + `"
    max_chat_id: 0
`
	cfgPath := writeConfig(t, dir, content)

	_, err := Load(cfgPath)
	if err == nil {
		t.Fatal("expected error for zero max_chat_id, got nil")
	}
}

func Test_InvalidYAML(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeConfig(t, dir, `{{{not valid yaml:::`)

	_, err := Load(cfgPath)
	if err == nil {
		t.Fatal("expected error for invalid YAML, got nil")
	}
}

func Test_MissingFile(t *testing.T) {
	_, err := Load("/tmp/nonexistent_config_test_file.yaml")
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}
