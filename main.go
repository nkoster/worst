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

	fmt.Println("Server running: http://localhost:3000")
	if err := http.ListenAndServe(":3000", nil); err != nil {
		log.Fatalf("Error starting server: %v", err)
	}
}

func uploadHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Only POST allowd", http.StatusMethodNotAllowed)
		return
	}

	const fileDir = "./files"
	_ = os.MkdirAll(fileDir, os.ModePerm)

	const maxUploadSize = 1024 * 1024 * 1024 // 1GB
	if err := r.ParseMultipartForm(maxUploadSize); err != nil {
		http.Error(w, "File too big", http.StatusBadRequest)
		return
	}

	file, fileHeader, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "Invalid file", http.StatusBadRequest)
		return
	}
	defer file.Close()

	fileName := filepath.Base(fileHeader.Filename)
	filePath := filepath.Join(fileDir, fileName)

	fileExt := strings.ToLower(filepath.Ext(fileName))
	allowedExts := map[string]bool{".mp3": true, ".wav": true, ".ogg": true}
	if _, ok := allowedExts[fileExt]; !ok {
		http.Error(w, "File format not allowed", http.StatusBadRequest)
		return
	}

	dst, err := os.Create(filePath)
	if err != nil {
		http.Error(w, "Cannot create file", http.StatusInternalServerError)
		return
	}
	defer dst.Close()

	if _, err := io.Copy(dst, file); err != nil {
		http.Error(w, "Cannot save file", http.StatusInternalServerError)
		return
	}

	ffmpegCmd := fmt.Sprintf("timeout --foreground 25 ffmpeg -i \"%s\" -af loudnorm=I=-16:dual_mono=true:TP=-1.5:LRA=11:print_format=summary -f null -", filePath)

	go func() {
		output, err := executeFFmpegCommand(ffmpegCmd)
		if err != nil {
			// ffmpeg error(s)
			log.Printf("ffmpeg error: %v\n%v", err, ffmpegCmd)
			commandOutputChan <- "ffmpeg error"
			return
		}
		commandOutputChan <- output
	}()

	w.Write([]byte("Upload ready\n"))
}

func eventsHandler(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming not supported", http.StatusInternalServerError)
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
	command := exec.Command("bash", "-c", cmd)
	var outputBuffer bytes.Buffer
	command.Stdout = &outputBuffer
	command.Stderr = &outputBuffer

	err := command.Run()
	output := outputBuffer.String()

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 124 {
			// Behandel exit status 124 (timeout) als een succesvolle afronding
			return processFFmpegOutput(output), nil
		}
		return processFFmpegOutput(output), fmt.Errorf("commando ready with error: %v, output: %s", err, output)
	}

	return processFFmpegOutput(output), nil
}

func processFFmpegOutput(output string) string {
	var result strings.Builder
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		if strings.Contains(line, "Input Integrated") || strings.Contains(line, "Output Integrated") || strings.Contains(line, "Input True Peak") || strings.Contains(line, "Output True Peak") || strings.Contains(line, "Input LRA") || strings.Contains(line, "Output LRA") || strings.Contains(line, "Input Threshold") || strings.Contains(line, "Output Threshold") || strings.Contains(line, "Normalization Type") || strings.Contains(line, "Target Offset") {
			result.WriteString(line + "\n")
		}
	}
	return result.String()
}
