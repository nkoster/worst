package main

import (
	"bytes"
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

	ffmpegCmd := fmt.Sprintf("2>&1 timeout --foreground 5 ffmpeg -i \"%s\" -af loudnorm=I=-16:dual_mono=true:TP=-1.5:LRA=11:print_format=summary -f null -", filePath)

	go func() {
		output, err := executeFFmpegCommand(ffmpegCmd)
		if err != nil {
			log.Printf("Fout bij het uitvoeren van ffmpeg commando: %v\n%v", err, ffmpegCmd)
			commandOutputChan <- "Fout bij het uitvoeren van ffmpeg commando"
			return
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

func executeFFmpegCommand(cmd string) (string, error) {
	// Voer het volledige commando uit binnen een bash shell
	command := exec.Command("bash", "-c", cmd)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr

	err := command.Run()

	// Verwerk de exit code 124 als succesvol (specifiek voor de timeout situatie)
	if exitErr, ok := err.(*exec.ExitError); ok {
		if exitErr.ExitCode() == 124 {
			return stdout.String(), nil // Beschouw timeout als een verwacht resultaat
		}
	}

	if err != nil {
		// In geval van een echte fout, retourneer ook stderr om het debuggen te vergemakkelijken
		return "", fmt.Errorf("fout bij het uitvoeren van commando: %v, stderr: %s", err, stderr.String())
	}

	return stdout.String(), nil
}
