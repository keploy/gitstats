package main

import (
	"log"
	"net/http"

	routes "github.com/sonichigo/gitstats/routes"
)

func main() {
	routes.SetupRoutes()
	port := "8080"

	log.Printf("Server starting on port %s", port)
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Fatalf("Error starting server: %v", err)
	}
}
