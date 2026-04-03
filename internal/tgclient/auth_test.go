package tgclient

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_BotConversationAuth_Phone(t *testing.T) {
	auth := NewBotConversationAuth("+7 (999) 123-45-67")
	phone, err := auth.Phone(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "+7(999)1234567", phone)
}

func Test_BotConversationAuth_PhoneNormalization(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"+1 800 555-1234", "+18005551234"},
		{"+7-999-123-45-67", "+79991234567"},
		{"  +49 30 12345  ", "+493012345"},
		{"+1234567890", "+1234567890"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.input, func(t *testing.T) {
			a := NewBotConversationAuth(tc.input)
			got, err := a.Phone(context.Background())
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func Test_BotConversationAuth_CodeBlocksUntilProvided(t *testing.T) {
	a := NewBotConversationAuth("+79991234567")

	const wantCode = "12345"
	go func() {
		time.Sleep(10 * time.Millisecond)
		a.ProvideCode(wantCode)
	}()

	ctx := context.Background()
	got, err := a.Code(ctx, nil)
	require.NoError(t, err)
	assert.Equal(t, wantCode, got)
}

func Test_BotConversationAuth_CodeTrimsSpace(t *testing.T) {
	a := NewBotConversationAuth("+79991234567")

	go func() {
		a.ProvideCode("  99999  ")
	}()

	got, err := a.Code(context.Background(), nil)
	require.NoError(t, err)
	assert.Equal(t, "99999", got)
}

func Test_BotConversationAuth_PasswordBlocksAndSetsFlag(t *testing.T) {
	a := NewBotConversationAuth("+79991234567")

	assert.False(t, a.NeedsPassword(), "NeedsPassword must be false before Password() is called")

	const wantPass = "s3cret!"
	go func() {
		time.Sleep(10 * time.Millisecond)
		a.ProvidePassword(wantPass)
	}()

	got, err := a.Password(context.Background())
	require.NoError(t, err)
	assert.Equal(t, wantPass, got)
	assert.True(t, a.NeedsPassword(), "NeedsPassword must be true after Password() is called")
}

func Test_BotConversationAuth_CodeCancelledByContext(t *testing.T) {
	a := NewBotConversationAuth("+79991234567")

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	_, err := a.Code(ctx, nil)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}
