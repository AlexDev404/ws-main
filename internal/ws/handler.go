package ws

// Filename: internal/ws/handler.go
import (
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

const (
	writeWait     = 5 * time.Second  // max time to complete a write
	idleReadLimit = 30 * time.Second // if we read nothing for 30s, time out
)

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
	// Limit message size
	conn.SetReadLimit(1024 * 4)
	// Idle timeout window starts now
	conn.SetReadDeadline(time.Now().Add(idleReadLimit))

	// Read/Echo loop
	for {
		msgType, payload, err := conn.ReadMessage()
		if err != nil {
			log.Printf("read error: %v", err)
			return
		}
		// Got a message - extend the deadline for the next read
		_ = conn.SetReadDeadline(time.Now().Add(idleReadLimit))
		// Write a message
		if msgType == websocket.TextMessage {
			_ = conn.SetWriteDeadline(time.Now().Add(writeWait))
			err := conn.WriteMessage(websocket.TextMessage, payload)
			if err != nil {
				log.Printf("write error: %v", err)
				return
			}
		}
	}
}
