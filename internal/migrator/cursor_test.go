package migrator

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSaveLoadRoundtrip(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "cursor.json")

	cm := NewCursorManager(filePath)
	cm.Update("test_chat", 42, 100, 50)

	if err := cm.Save(); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	loaded := NewCursorManager(filePath)
	if err := loaded.Load(); err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if got := loaded.GetLastMessageID("test_chat"); got != 42 {
		t.Errorf("LastMessageID = %d, want 42", got)
	}
}

func TestLoadNonexistentFile(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "nonexistent.json")

	cm := NewCursorManager(filePath)
	if err := cm.Load(); err != nil {
		t.Fatalf("Load on nonexistent file should return nil, got: %v", err)
	}

	if got := cm.GetLastMessageID("any"); got != 0 {
		t.Errorf("GetLastMessageID on fresh manager = %d, want 0", got)
	}
}

func TestLoadCorruptedJSON(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "cursor.json")

	if err := os.WriteFile(filePath, []byte("{not valid json!!!"), 0644); err != nil {
		t.Fatalf("write garbage file: %v", err)
	}

	cm := NewCursorManager(filePath)
	if err := cm.Load(); err == nil {
		t.Fatal("Load on corrupted JSON should return error, got nil")
	}
}

func TestLoadEmptyFile(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "cursor.json")

	if err := os.WriteFile(filePath, []byte(""), 0644); err != nil {
		t.Fatalf("write empty file: %v", err)
	}

	cm := NewCursorManager(filePath)
	if err := cm.Load(); err == nil {
		t.Fatal("Load on empty file should return error, got nil")
	}
}

func TestSaveCreatesFile(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "cursor.json")

	cm := NewCursorManager(filePath)
	cm.Update("chat", 1, 10, 5)

	if err := cm.Save(); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	if _, err := os.Stat(filePath); err != nil {
		t.Fatalf("file should exist after Save, stat error: %v", err)
	}
}

func TestMultipleCursors(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "cursor.json")

	cm := NewCursorManager(filePath)
	cm.Update("chat_alpha", 10, 100, 50)
	cm.Update("chat_beta", 20, 200, 100)

	if err := cm.Save(); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	loaded := NewCursorManager(filePath)
	if err := loaded.Load(); err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	tests := []struct {
		chatName string
		wantID   int
	}{
		{"chat_alpha", 10},
		{"chat_beta", 20},
	}

	for _, tt := range tests {
		if got := loaded.GetLastMessageID(tt.chatName); got != tt.wantID {
			t.Errorf("GetLastMessageID(%q) = %d, want %d", tt.chatName, got, tt.wantID)
		}
	}
}

func TestGetLastMessageID(t *testing.T) {
	tests := []struct {
		name     string
		chatName string
		setup    func(cm *CursorManager)
		want     int
	}{
		{
			name:     "unknown chat returns 0",
			chatName: "unknown",
			setup:    func(cm *CursorManager) {},
			want:     0,
		},
		{
			name:     "known chat returns correct ID",
			chatName: "known",
			setup: func(cm *CursorManager) {
				cm.Update("known", 77, 500, 300)
			},
			want: 77,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cm := NewCursorManager(filepath.Join(t.TempDir(), "cursor.json"))
			tt.setup(cm)

			if got := cm.GetLastMessageID(tt.chatName); got != tt.want {
				t.Errorf("GetLastMessageID(%q) = %d, want %d", tt.chatName, got, tt.want)
			}
		})
	}
}

func TestUpdateOverwrites(t *testing.T) {
	cm := NewCursorManager(filepath.Join(t.TempDir(), "cursor.json"))

	cm.Update("chat", 1, 10, 5)
	cm.Update("chat", 99, 1000, 500)

	if got := cm.GetLastMessageID("chat"); got != 99 {
		t.Errorf("after overwrite, GetLastMessageID = %d, want 99", got)
	}
}
