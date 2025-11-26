package ws

// Filename: internal/ws/handler.go

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

// Heartbeat and timeout settings
const (
	writeWait  = 5 * time.Second     // max time to complete a write
	pongWait   = 30 * time.Second    // if we don't get a pong in 30s, time out
	pingPeriod = (pongWait * 9) / 10 // send pings at ~90% of pongWait (e.g., 27s)
)

// reverseString reverses a string handling Unicode properly
func reverseString(s string) string {
	runes := []rune(s)
	for i, j := 0, len(runes)-1; i < j; i, j = i+1, j-1 {
		runes[i], runes[j] = runes[j], runes[i]
	}
	return string(runes)
}

// processCommand processes JSON commands and returns JSON response
func processCommand(payload []byte) ([]byte, error) {
	var req CommandRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		resp := CommandResponse{
			Error: "Invalid JSON",
		}
		return json.Marshal(resp)
	}

	var result float64
	var errMsg string

	switch req.Command {
	case "add":
		result = req.A + req.B
	case "subtract":
		result = req.A - req.B
	case "multiply":
		result = req.A * req.B
	case "divide":
		if math.Abs(req.B) < 1e-9 {
			errMsg = "Division by zero"
		} else {
			result = req.A / req.B
		}
	default:
		errMsg = "Unknown command"
	}

	resp := CommandResponse{
		Result:  result,
		Command: req.Command,
		Error:   errMsg,
	}

	return json.Marshal(resp)
}

// Only allow pages served from this origin to connect
var allowedOrigins = []string{
	"http://localhost:4000",
}

func originAllowed(o string) bool {
	if o == "" {
		return false
	}
	for _, a := range allowedOrigins {
		if strings.EqualFold(o, a) {
			return true
		}
	}
	return false
}

// The upgrader object is used when we need to upgrade from HTTP to RFC 6455
var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		ok := originAllowed(origin)
		if !ok {
			log.Printf("blocked cross-origin websocket: Origin=%q Path=%s", origin, r.URL.Path)
		}
		return ok
	},
	Error: func(w http.ResponseWriter, r *http.Request, status int, reason error) {
		http.Error(w, "origin not allowed", http.StatusForbidden)
	},
}

// Attempt to upgrade from HTTP to RFC 6455
func HandleWebSocket(w http.ResponseWriter, r *http.Request) {
	// Has to be an HTTP GET request
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Upgrade the connection from HTTP to RFC 6455
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("upgrade error: %v", err)
		return
	}
	defer conn.Close()

	log.Printf("connection opened from %s", r.RemoteAddr)

	// Limit message size
	conn.SetReadLimit(1024 * 4)

	// PING / PONG SETUP

	// Idle timeout window starts now: must receive a pong within pongWait
	_ = conn.SetReadDeadline(time.Now().Add(pongWait))

	// On each pong, extend the read deadline again
	conn.SetPongHandler(func(appData string) error {
		_ = conn.SetReadDeadline(time.Now().Add(pongWait))
		log.Printf("pong from %s (data=%q)", r.RemoteAddr, appData)
		return nil
	})

	// Start a goroutine that sends pings every pingPeriod
	done := make(chan struct{})
	ticker := time.NewTicker(pingPeriod)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				// Send a ping; if this fails, the read loop will notice soon
				_ = conn.SetWriteDeadline(time.Now().Add(writeWait))
				if err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(writeWait)); err != nil {
					log.Printf("ping write error: %v", err)
					return
				}
				log.Printf("ping → %s", r.RemoteAddr)
			case <-done:
				return
			}
		}
	}()

	// Read/Echo loop
	for {
		msgType, payload, err := conn.ReadMessage()
		if err != nil {
			// This error will be:
			//  - a timeout (no pong in time), or
			//  - a normal close, or
			//  - some other read error
			log.Printf("read error (timeout/close): %v", err)

			// Try to send a graceful close so the client can see 1000 instead of 1006
			_ = conn.SetWriteDeadline(time.Now().Add(writeWait))
			_ = conn.WriteControl(
				websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.CloseNormalClosure, "idle timeout"),
				time.Now().Add(writeWait),
			)

			break
		}

		// We successfully read a message; normal traffic also keeps the connection alive.
		// Note: the pong handler also updates the read deadline on pongs.

		// Echo back text messages
		if msgType == websocket.TextMessage {
			_ = conn.SetWriteDeadline(time.Now().Add(writeWait))
			// Part 3: Increment the message counter atomically
			count := atomic.AddUint64(&messageCounter, 1)

			var responsePayload []byte

			// Part 4: Check if payload is valid JSON by attempting to unmarshal
			if len(payload) > 0 && payload[0] == '{' {
				var testJSON map[string]interface{}
				if json.Unmarshal(payload, &testJSON) == nil {
					// Valid JSON - process as command
					jsonResponse, err := processCommand(payload)
					if err != nil {
						log.Printf("JSON processing error: %v", err)
						responsePayload = []byte(fmt.Sprintf("[Msg #%d] Error processing command", count))
					} else {
						// For JSON commands, we don't add the message counter prefix
						responsePayload = jsonResponse
					}
				} else {
					// Invalid JSON - treat as normal text message
					responsePayload = []byte(fmt.Sprintf("[Msg #%d] %s", count, payload))
				}
			} else {
				// Part 1: Check if the message starts with "UPPER:"
				message := string(payload)
				if strings.HasPrefix(message, "UPPER:") {
					// Extract the rest and convert to uppercase
					text := strings.TrimPrefix(message, "UPPER:")
					payload = []byte(strings.ToUpper(text))
				} else if strings.HasPrefix(message, "REVERSE:") {
					// Part 2: Check if the message starts with "REVERSE:"
					text := strings.TrimPrefix(message, "REVERSE:")
					payload = []byte(reverseString(text))
				}

				// Part 3: Format the response with the counter
				responsePayload = []byte(fmt.Sprintf("[Msg #%d] %s", count, payload))
			}

			if err := conn.WriteMessage(websocket.TextMessage, responsePayload); err != nil {
				log.Printf("write error: %v", err)
				break
			}
		}
	}

	// Stop the ping goroutine
	close(done)

	log.Printf("connection closed from %s", r.RemoteAddr)
}
