package admin

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// SSEBroker manages Server-Sent Events clients and broadcasts messages.
type SSEBroker struct {
	clients map[chan string]struct{}
	mu      sync.RWMutex
}

func newSSEBroker() *SSEBroker {
	return &SSEBroker{
		clients: make(map[chan string]struct{}),
	}
}

// Subscribe registers a new client and returns a channel for receiving events.
func (b *SSEBroker) Subscribe() chan string {
	ch := make(chan string, 8)
	b.mu.Lock()
	b.clients[ch] = struct{}{}
	b.mu.Unlock()
	return ch
}

// Unsubscribe removes a client channel.
func (b *SSEBroker) Unsubscribe(ch chan string) {
	b.mu.Lock()
	delete(b.clients, ch)
	close(ch)
	b.mu.Unlock()
}

// Broadcast sends an SSE event to all connected clients.
// Non-blocking: drops message if client buffer is full.
func (b *SSEBroker) Broadcast(event, data string) {
	msg := fmt.Sprintf("event: %s\ndata: %s\n\n", event, data)
	b.mu.RLock()
	defer b.mu.RUnlock()
	for ch := range b.clients {
		select {
		case ch <- msg:
		default:
			// client too slow, skip
		}
	}
}

// ClientCount returns the number of connected SSE clients.
func (b *SSEBroker) ClientCount() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.clients)
}

// handleSSE serves the SSE endpoint for live dashboard updates.
func (s *Server) handleSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "SSE not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // disable nginx/reverse-proxy buffering

	ch := s.broker.Subscribe()
	defer s.broker.Unsubscribe(ch)

	for {
		select {
		case <-r.Context().Done():
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			fmt.Fprint(w, msg)
			flusher.Flush()
		}
	}
}

// sseLoop periodically renders partials and broadcasts them to all SSE clients.
func (s *Server) sseLoop(ctx context.Context) {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	tick := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if s.broker.ClientCount() == 0 {
				continue // no clients connected, skip work
			}

			tick++

			// Active migration: every tick (3s)
			var activeBuf bytes.Buffer
			if err := s.tmpl.ExecuteTemplate(&activeBuf, "active_migration", pageData{LiveMig: s.bot.ActiveMigration()}); err != nil {
				s.log.Error("sse: render active_migration failed", "error", err)
				continue
			}
			s.broker.Broadcast("active", activeBuf.String())

			// Stats + recent: every 6th tick (18s)
			if tick%6 == 0 {
				stats, err := s.store.GetStats(ctx)
				if err != nil {
					s.log.Warn("sse: get stats failed", "error", err)
				}
				var statsBuf bytes.Buffer
				if err := s.tmpl.ExecuteTemplate(&statsBuf, "stats_cards", pageData{Stats: stats}); err != nil {
					s.log.Error("sse: render stats_cards failed", "error", err)
				} else {
					s.broker.Broadcast("stats", statsBuf.String())
				}

				recent, err := s.store.GetRecentMigrations(ctx, 5)
				if err != nil {
					s.log.Warn("sse: get recent migrations failed", "error", err)
				}
				var recentBuf bytes.Buffer
				if err := s.tmpl.ExecuteTemplate(&recentBuf, "recent_migrations", pageData{Recent: recent}); err != nil {
					s.log.Error("sse: render recent_migrations failed", "error", err)
				} else {
					s.broker.Broadcast("recent", recentBuf.String())
				}
			}
		}
	}
}
