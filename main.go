package main

import (
	"fmt"
	"log"
	"net/http"
	"os"

	"matching-engine/api"
	"matching-engine/engine"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	ob := engine.NewOrderBook()
	handler := api.NewServer(ob)

	addr := fmt.Sprintf(":%s", port)
	log.Printf("listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, handler))
}
