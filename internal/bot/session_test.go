package bot

import "testing"

func TestGetActiveMigration_NoActive(t *testing.T) {
	store := NewSessionStore()
	store.GetOrCreate(1).State = StateIdle
	store.GetOrCreate(2).State = StateAwaitingConfirm

	if got := store.GetActiveMigration(); got != nil {
		t.Errorf("expected nil, got session for user %d", got.UserID)
	}
}

func TestGetActiveMigration_Migrating(t *testing.T) {
	store := NewSessionStore()
	store.GetOrCreate(1).State = StateIdle
	sess2 := store.GetOrCreate(2)
	sess2.State = StateMigrating

	got := store.GetActiveMigration()
	if got == nil {
		t.Fatal("expected active session, got nil")
	}
	if got.UserID != 2 {
		t.Errorf("UserID = %d, want 2", got.UserID)
	}
}

func TestGetActiveMigration_Paused(t *testing.T) {
	store := NewSessionStore()
	sess := store.GetOrCreate(1)
	sess.State = StatePaused

	got := store.GetActiveMigration()
	if got == nil {
		t.Fatal("expected active session for paused migration, got nil")
	}
	if got.UserID != 1 {
		t.Errorf("UserID = %d, want 1", got.UserID)
	}
}
