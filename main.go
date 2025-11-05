package main

import (
	"log"
	"net/http"

	"github.com/alexdev404/ws-main/internal/ws"
)

func handleHome(w http.ResponseWriter, r *http.Request) {
	w.Write([]byte("Welcome to the WebSocket server!"))
}

func main() {
	mux := http.NewServeMux()

	mux.HandleFunc("/", handleHome)
	mux.HandleFunc("/ws", ws.HandleWebsockets)
	http.ListenAndServe(":4000", mux)

	log.Println("Server started on :4000")
	err := http.ListenAndServe(":4000", mux)
	if err != nil {
		log.Fatal("ListenAndServe: ", err)
	}
}
