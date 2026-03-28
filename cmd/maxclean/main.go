package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	maxbotapi "github.com/max-messenger/max-bot-api-client-go"
)

func main() {
	token := os.Getenv("MAX_TOKEN")
	if token == "" {
		fmt.Fprintln(os.Stderr, "MAX_TOKEN env required")
		os.Exit(1)
	}

	search := ""
	if len(os.Args) > 1 {
		search = strings.ToLower(os.Args[1])
	}

	api, err := maxbotapi.New(token, maxbotapi.WithApiTimeout(30*time.Second))
	if err != nil {
		fmt.Fprintf(os.Stderr, "api: %v\n", err)
		os.Exit(1)
	}

	ctx := context.Background()

	list, err := api.Chats.GetChats(ctx, 50, 0)
	if err != nil {
		fmt.Fprintf(os.Stderr, "get chats: %v\n", err)
		os.Exit(1)
	}

	if search == "" {
		fmt.Println("Chats:")
		for _, c := range list.Chats {
			fmt.Printf("  [%d] %s (type=%s, status=%s)\n", c.ChatId, c.Title, c.Type, c.Status)
		}
		fmt.Println("\nUsage: maxclean <chat_name_substring>")
		return
	}

	var chatID int64
	var chatTitle string
	for _, c := range list.Chats {
		if strings.Contains(strings.ToLower(c.Title), search) {
			chatID = c.ChatId
			chatTitle = c.Title
			break
		}
	}
	if chatID == 0 {
		fmt.Fprintf(os.Stderr, "chat %q not found\n", search)
		os.Exit(1)
	}

	fmt.Printf("Cleaning chat: %s (ID: %d)\n", chatTitle, chatID)

	msgs, err := api.Messages.GetMessages(ctx, chatID, nil, 0, 0, 100)
	if err != nil {
		fmt.Fprintf(os.Stderr, "get messages: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Found %d messages\n", len(msgs.Messages))

	deleted := 0
	for _, m := range msgs.Messages {
		_, err := api.Messages.DeleteMessage(ctx, m.Body.Mid)
		if err != nil {
			fmt.Printf("  skip %s: %v\n", m.Body.Mid, err)
			continue
		}
		deleted++
		time.Sleep(500 * time.Millisecond)
	}

	fmt.Printf("Deleted: %d\n", deleted)
}
