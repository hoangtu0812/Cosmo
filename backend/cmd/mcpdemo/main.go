// Command mcpdemo runs the neutral MCP conformance server used to verify
// Cosmo without making SAP, or any other business integration, its test oracle.
package main

import (
	"log"
	"net/http"
	"os"

	"cosmo/backend/internal/mcpdemo"
)

func main() {
	address := os.Getenv("MCPDEMO_ADDRESS")
	if address == "" {
		address = ":8090"
	}

	log.Printf("mcpdemo listening on %s", address)
	if err := http.ListenAndServe(address, mcpdemo.Handler()); err != nil {
		log.Fatal(err)
	}
}
