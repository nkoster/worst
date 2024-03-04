package main

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

var (
	// Global channel voor het versturen van commando-output naar SSE clients
	commandOutputChan = make(chan string)
)

func main() {
	http.HandleFunc("/upload", uploadHandler)
	http.HandleFunc("/events", eventsHandler)
	http.Handle("/", http.FileServer(http.Dir("./ui")))

	fmt.Println("Server gestart op http://localhost:3000")
	if err := http.ListenAndServe(":3000", nil); err != nil {
		log.Fatalf("Fout bij het starten van de server: %v", err)
	}
}

func uploadHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Alleen POST is toegestaan", http.StatusMethodNotAllowed)
		return
	}

	const fileDir = "./files"
	_ = os.MkdirAll(fileDir, os.ModePerm)

	const maxUploadSize = 1024 * 1024 * 1024 // 1GB
	if err := r.ParseMultipartForm(maxUploadSize); err != nil {
		http.Error(w, "Het bestand is te groot", http.StatusBadRequest)
		return
	}

	file, fileHeader, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "Ongeldig bestand", http.StatusBadRequest)
		return
	}
	defer file.Close()

	fileName := filepath.Base(fileHeader.Filename)
	filePath := filepath.Join(fileDir, fileName)

	fileExt := strings.ToLower(filepath.Ext(fileName))
	allowedExts := map[string]bool{".mp3": true, ".wav": true, ".ogg": true}
	if _, ok := allowedExts[fileExt]; !ok {
		http.Error(w, "Ongeldig bestandsformaat", http.StatusBadRequest)
		return
	}

	dst, err := os.Create(filePath)
	if err != nil {
		http.Error(w, "Fout bij het aanmaken van bestand", http.StatusInternalServerError)
		return
	}
	defer dst.Close()

	if _, err := io.Copy(dst, file); err != nil {
		http.Error(w, "Fout bij het opslaan van bestand", http.StatusInternalServerError)
		return
	}

	// Uitvoeren van het commando na succesvolle upload
	go func() {
		output, err := executeCommand("ls", []string{"-l", filePath})
		if err != nil {
			log.Printf("Fout bij het uitvoeren van commando: %v", err)
			output = "Fout bij het uitvoeren van commando"
		}
		commandOutputChan <- output
	}()

	w.Write([]byte("Bestand succesvol geüpload\n"))
}

func eventsHandler(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming wordt niet ondersteund", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	for {
		select {
		case output := <-commandOutputChan:
			fmt.Fprintf(w, "data: %s\n\n", strings.ReplaceAll(output, "\n", "\\n"))
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

func executeCommand(command string, args []string) (string, error) {
	cmd := exec.Command(command, args...)
	output, err := cmd.CombinedOutput()
	return string(output), err
}
