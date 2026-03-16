package migrator

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type Cursor struct {
	ChatName      string    `json:"chat_name"`
	LastMessageID int       `json:"last_message_id"`
	TotalMessages int       `json:"total_messages"`
	SentMessages  int       `json:"sent_messages"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type CursorManager struct {
	filePath string
	cursors  map[string]Cursor
}

func NewCursorManager(filePath string) *CursorManager {
	return &CursorManager{
		filePath: filePath,
		cursors:  make(map[string]Cursor),
	}
}

func (cm *CursorManager) Load() error {
	data, err := os.ReadFile(cm.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read cursor file: %w", err)
	}

	var cursors []Cursor
	if err := json.Unmarshal(data, &cursors); err != nil {
		return fmt.Errorf("parse cursor file: %w", err)
	}

	for _, c := range cursors {
		cm.cursors[c.ChatName] = c
	}
	return nil
}

func (cm *CursorManager) Save() error {
	cursors := make([]Cursor, 0, len(cm.cursors))
	for _, c := range cm.cursors {
		cursors = append(cursors, c)
	}

	data, err := json.MarshalIndent(cursors, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal cursors: %w", err)
	}

	dir := filepath.Dir(cm.filePath)
	tmp, err := os.CreateTemp(dir, ".cursor-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp cursor file: %w", err)
	}
	tmpPath := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("write temp cursor file: %w", err)
	}

	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("close temp cursor file: %w", err)
	}

	if err := os.Rename(tmpPath, cm.filePath); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("rename temp cursor file: %w", err)
	}

	return nil
}

func (cm *CursorManager) GetLastMessageID(chatName string) int {
	if c, ok := cm.cursors[chatName]; ok {
		return c.LastMessageID
	}
	return 0
}

func (cm *CursorManager) Update(chatName string, msgID, total, sent int) {
	cm.cursors[chatName] = Cursor{
		ChatName:      chatName,
		LastMessageID: msgID,
		TotalMessages: total,
		SentMessages:  sent,
		UpdatedAt:     time.Now(),
	}
}
