package web

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/ushineko/face-puncher-supreme/internal/logbuf"
	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

// wsMessage is the envelope for all WebSocket messages.
type wsMessage struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data,omitempty"`
}

// clientMessage is what the client sends to the server.
type clientMessage struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data,omitempty"`
}

// client represents a single WebSocket connection.
type client struct {
	conn     *websocket.Conn
	send     chan []byte
	logLevel slog.Level
	mu       sync.Mutex
}

func (c *client) setLogLevel(level slog.Level) {
	c.mu.Lock()
	c.logLevel = level
	c.mu.Unlock()
}

func (c *client) getLogLevel() slog.Level {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.logLevel
}

// Hub manages WebSocket clients and broadcasts messages.
type Hub struct {
	clients    map[*client]struct{}
	register   chan *client
	unregister chan *client
	mu         sync.Mutex

	heartbeatFn func() ([]byte, error)
	statsFn     func() ([]byte, error)
	reloadFn    func() error
	logBuffer   *logbuf.Buffer
	logSub      *logbuf.Subscriber
	logger      *slog.Logger

	done chan struct{}
}

func newHub(heartbeatFn, statsFn func() ([]byte, error), reloadFn func() error, logBuf *logbuf.Buffer, logger *slog.Logger) *Hub {
	return &Hub{
		clients:     make(map[*client]struct{}),
		register:    make(chan *client),
		unregister:  make(chan *client),
		heartbeatFn: heartbeatFn,
		statsFn:     statsFn,
		reloadFn:    reloadFn,
		logBuffer:   logBuf,
		logSub:      logBuf.Subscribe(slog.LevelDebug), // capture all, filter per-client
		logger:      logger,
		done:        make(chan struct{}),
	}
}

func (h *Hub) run() {
	statsTicker := time.NewTicker(3 * time.Second)
	heartbeatTicker := time.NewTicker(5 * time.Second)
	defer statsTicker.Stop()
	defer heartbeatTicker.Stop()

	for {
		select {
		case <-h.done:
			return

		case c := <-h.register:
			h.mu.Lock()
			h.clients[c] = struct{}{}
			h.mu.Unlock()

		case c := <-h.unregister:
			h.mu.Lock()
			if _, ok := h.clients[c]; ok {
				delete(h.clients, c)
				close(c.send)
			}
			h.mu.Unlock()

		case <-heartbeatTicker.C:
			data, err := h.heartbeatFn()
			if err != nil {
				h.logger.Error("heartbeat build failed", "error", err)
				continue
			}
			h.broadcast(marshalWSMessage("heartbeat", data))

		case <-statsTicker.C:
			data, err := h.statsFn()
			if err != nil {
				h.logger.Error("stats build failed", "error", err)
				continue
			}
			h.broadcast(marshalWSMessage("stats", data))

		case entry := <-h.logSub.C:
			data, _ := json.Marshal(entry) //nolint:errcheck // best-effort marshal
			msg := marshalWSMessage("log", data)
			entryLevel := logbuf.ParseLevel(entry.Level)

			h.mu.Lock()
			for c := range h.clients {
				if entryLevel >= c.getLogLevel() {
					select {
					case c.send <- msg:
					default:
					}
				}
			}
			h.mu.Unlock()
		}
	}
}

func (h *Hub) stop() {
	close(h.done)
	h.logBuffer.Unsubscribe(h.logSub)
}

func (h *Hub) broadcast(msg []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.clients {
		select {
		case c.send <- msg:
		default:
			// Slow client — drop message.
		}
	}
}

func marshalWSMessage(msgType string, data json.RawMessage) []byte {
	msg := wsMessage{Type: msgType, Data: data}
	b, _ := json.Marshal(msg) //nolint:errcheck // best-effort marshal
	return b
}

// handleWebSocket upgrades the HTTP connection to WebSocket and manages the client.
func (s *DashboardServer) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true, // same-origin only, behind proxy
	})
	if err != nil {
		s.logger.Error("websocket accept failed", "error", err)
		return
	}

	c := &client{
		conn:     conn,
		send:     make(chan []byte, 256),
		logLevel: slog.LevelInfo,
	}

	s.hub.register <- c

	// Write pump.
	go func() {
		defer func() {
			s.hub.unregister <- c
			conn.Close(websocket.StatusNormalClosure, "") //nolint:errcheck,gosec // best-effort close // best-effort close
		}()
		for msg := range c.send {
			ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
			err := conn.Write(ctx, websocket.MessageText, msg)
			cancel()
			if err != nil {
				return
			}
		}
	}()

	// Read pump — handles client messages.
	for {
		var msg clientMessage
		err := wsjson.Read(r.Context(), conn, &msg)
		if err != nil {
			break
		}

		switch msg.Type {
		case "reload":
			go s.handleReload(c)
		case "set_log_level":
			var data struct {
				MinLevel string `json:"min_level"`
			}
			if json.Unmarshal(msg.Data, &data) == nil {
				level := logbuf.ParseLevel(data.MinLevel)
				c.setLogLevel(level)
			}
		}
	}
}

// handleReload executes the reload and pushes the result to the requesting client.
func (s *DashboardServer) handleReload(c *client) {
	var result struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
	}

	if s.reloadFn == nil {
		result.Message = "reload not supported"
	} else if err := s.reloadFn(); err != nil {
		result.Message = err.Error()
	} else {
		result.Success = true
		result.Message = "configuration reloaded successfully"
	}

	data, _ := json.Marshal(result) //nolint:errcheck // best-effort marshal
	msg := marshalWSMessage("reload_result", data)
	select {
	case c.send <- msg:
	default:
	}
}
