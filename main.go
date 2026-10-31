// Gallery is a small self-hosted photo gallery in the spirit of Piwigo:
// nested albums, tags, search, thumbnails, uploads and a password-protected
// admin area. It uses only the Go standard library.
package main

import (
	"crypto/rand"
	"encoding/hex"
	"flag"
	"log"
	"net/http"
	"os"
	"time"
)

func main() {
	addr := flag.String("addr", ":8080", "listen address")
	dataDir := flag.String("data", "data", "directory for photos, thumbnails and the database")
	importDir := flag.String("import", "", "import a folder tree of images as albums, then exit")
	flag.Parse()

	password := os.Getenv("GALLERY_PASSWORD")
	if password == "" {
		b := make([]byte, 6)
		rand.Read(b)
		password = hex.EncodeToString(b)
		log.Printf("GALLERY_PASSWORD not set; admin password for this run: %s", password)
	}

	srv, err := NewServer(*dataDir, password)
	if err != nil {
		log.Fatal(err)
	}

	if *importDir != "" {
		added, skipped, err := srv.ImportDir(*importDir)
		if err != nil {
			log.Fatal(err)
		}
		log.Printf("import finished: %d added, %d skipped", added, skipped)
		return
	}

	hs := &http.Server{
		Addr:              *addr,
		Handler:           srv.Routes(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	log.Printf("Gallery listening on %s (data in %s)", *addr, *dataDir)
	log.Fatal(hs.ListenAndServe())
}
