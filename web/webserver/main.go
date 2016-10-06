package main

import (
	"log"
	"net/http"
	"os"

	"github.com/nitrous-io/nfx-depbuilder/web"
	"github.com/nitrous-io/nfx-depbuilder/web/config"
)

func main() {
	addr := os.Getenv("NFX_WEB_ADDR")
	if addr == "" {
		addr = ":3333"
	}

	r := web.AppRouter()

	if err := config.Validate(); err != nil {
		log.Fatalf("invalid config: %v", err)
	}

	http.ListenAndServe(addr, r)
}
