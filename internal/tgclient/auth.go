package tgclient

import (
	"context"
	"fmt"
	"strings"

	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/tg"
)

// BotConversationAuth implements auth.UserAuthenticator by receiving
// phone, code, and password through Go channels. This allows the Telegram bot
// to feed user input from chat messages into the MTProto auth flow.
type BotConversationAuth struct {
	phone    string
	codeCh   chan string
	passCh   chan string
	errCh    chan error
	needPass bool // set to true if 2FA is required
}

// NewBotConversationAuth creates a new auth coordinator.
// The phone number is provided upfront; code and password are received asynchronously.
func NewBotConversationAuth(phone string) *BotConversationAuth {
	return &BotConversationAuth{
		phone:  normalizePhone(phone),
		codeCh: make(chan string, 1),
		passCh: make(chan string, 1),
		errCh:  make(chan error, 1),
	}
}

// ProvideCode sends the auth code received from the user.
func (a *BotConversationAuth) ProvideCode(code string) {
	a.codeCh <- strings.TrimSpace(code)
}

// ProvidePassword sends the 2FA password received from the user.
func (a *BotConversationAuth) ProvidePassword(password string) {
	a.passCh <- password
}

// NeedsPassword returns true if the auth flow requires a 2FA password.
func (a *BotConversationAuth) NeedsPassword() bool {
	return a.needPass
}

// Cancel signals that the user wants to abort the auth flow.
func (a *BotConversationAuth) Cancel() {
	a.errCh <- fmt.Errorf("auth cancelled by user")
}

// --- auth.UserAuthenticator interface ---

func (a *BotConversationAuth) Phone(_ context.Context) (string, error) {
	return a.phone, nil
}

func (a *BotConversationAuth) Code(ctx context.Context, _ *tg.AuthSentCode) (string, error) {
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case code := <-a.codeCh:
		return code, nil
	case err := <-a.errCh:
		return "", err
	}
}

func (a *BotConversationAuth) Password(ctx context.Context) (string, error) {
	a.needPass = true
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case pass := <-a.passCh:
		return pass, nil
	case err := <-a.errCh:
		return "", err
	}
}

func (a *BotConversationAuth) AcceptTermsOfService(_ context.Context, _ tg.HelpTermsOfService) error {
	return nil
}

func (a *BotConversationAuth) SignUp(_ context.Context) (auth.UserInfo, error) {
	return auth.UserInfo{}, fmt.Errorf("sign up not supported, use existing account")
}

// normalizePhone strips spaces and dashes from a phone number.
func normalizePhone(phone string) string {
	phone = strings.TrimSpace(phone)
	phone = strings.ReplaceAll(phone, " ", "")
	phone = strings.ReplaceAll(phone, "-", "")
	return phone
}
