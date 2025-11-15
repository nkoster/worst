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
	"time"

	"github.com/joho/godotenv"
)

var (
	// Global channel voor het versturen van commando-output naar SSE clients
	commandOutputChan = make(chan string)
)

var fileDir string
var graphColor string

func main() {

	err := godotenv.Load()
	if err != nil {
		log.Fatal("Error loading .env file")
	}

	uploadURL := os.Getenv("UPLOAD_URL")
	if uploadURL == "" {
		uploadURL = "/upload"
	}
	http.HandleFunc(uploadURL, uploadHandler)
	fmt.Println("Upload URL: ", uploadURL)

	eventsURL := os.Getenv("EVENTS_URL")
	if eventsURL == "" {
		eventsURL = "/events"
	}

	fileDir = os.Getenv("FILE_DIR")
	if fileDir == "" {
		fileDir = "./ui/files"
	}
	fmt.Println("File directory: ", fileDir)

	http.HandleFunc(eventsURL, eventsHandler)
	fmt.Println("Events URL: ", eventsURL)

	directoryUI := os.Getenv("UI")
	if directoryUI == "" {
		directoryUI = "./ui"
	}

	graphColor = os.Getenv("GRAPH_COLOR")
	if graphColor == "" {
		graphColor = "0099CE"
	}
	fmt.Println("Graph color: ", graphColor)

	http.Handle("/", http.FileServer(http.Dir(directoryUI)))
	fmt.Println("UI directory: ", directoryUI)

	fmt.Println("Server running: http://localhost:3456")
	if err := http.ListenAndServe(":3456", nil); err != nil {
		log.Fatalf("Error starting server: %v", err)
	}
}

func uploadHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Only POST allowd", http.StatusMethodNotAllowed)
		return
	}

	_ = os.MkdirAll(fileDir, os.ModePerm)

	const maxUploadSize = 1024 * 1024 * 1024 // 1GB
	if err := r.ParseMultipartForm(maxUploadSize); err != nil {
		fmt.Println("ERROR: File too big.")
		http.Error(w, "File too big", http.StatusBadRequest)
		return
	}

	file, fileHeader, err := r.FormFile("file")
	if err != nil {
		fmt.Println("ERROR: Invalid file: ", err)
		http.Error(w, "Invalid file", http.StatusBadRequest)
		return
	}
	defer file.Close()

	fileName := filepath.Base(fileHeader.Filename)
	filePath := filepath.Join(fileDir, fileName)

	fileExt := strings.ToLower(filepath.Ext(fileName))
	// .mp3,.wav,.ogg,.aiff,.aac,.m4a,.opus,.flac,.wma
	allowedExts := map[string]bool{".mp3": true, ".wav": true, ".ogg": true, ".aiff": true, ".aac": true, ".m4a": true, ".opus": true, ".flac": true, ".wma": true}
	if _, ok := allowedExts[fileExt]; !ok {
		fmt.Println("ERROR: File format not allowed.")
		http.Error(w, "File format not allowed", http.StatusBadRequest)
		return
	}

	dst, err := os.Create(filePath)
	if err != nil {
		fmt.Println("ERROR: Cannot create file: ", err)
		http.Error(w, "Cannot create file", http.StatusInternalServerError)
		return
	}
	defer dst.Close()

	if _, err := io.Copy(dst, file); err != nil {
		fmt.Println("ERROR: Cannot save file: ", err)
		http.Error(w, "Cannot save file", http.StatusInternalServerError)
		return
	}

	fmt.Println("File uploaded: ", filePath)
	// TODO: Dynamic ffmpeg command args
	// ffmpegCmd := fmt.Sprintf("timeout --foreground 25 ffmpeg -i \"%s\" -af loudnorm=I=-16:dual_mono=true:TP=-1.5:LRA=11:print_format=summary -f null -", filePath) kleurcode: 0099CE
	ffmpegCmd := fmt.Sprintf("ffmpeg -i \"%s\" -y -filter_complex \"aformat=channel_layouts=stereo,showwavespic=s=700x120:colors=%s|0000000\" -frames:v 1 \"%s.png\" -af loudnorm=I=-16:dual_mono=true:TP=-1.5:LRA=11:print_format=summary -f null -", filePath, graphColor, filePath)

	go func() {
		output, err := executeFFmpegCommand(ffmpegCmd)
		if err != nil {
			// ffmpeg error(s)
			fmt.Println("ERROR: ", err)
			log.Printf("ffmpeg error: %v\n%v", err, ffmpegCmd)
			commandOutputChan <- "ffmpeg error"
			return
		}
		commandOutputChan <- output
	}()
	// Firefox hack: voeg een extra newline toe om de SSE-verbinding te forceren
	commandOutputChan <- "\n"
	w.Write([]byte("Upload ready\n"))
}

func eventsHandler(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		fmt.Println("ERROR: Streaming not supported")
		http.Error(w, "Streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case output := <-commandOutputChan:
			fmt.Fprintf(w, "data: %s\n\n", strings.ReplaceAll(output, "\n", "\\n"))
			flusher.Flush()
		case <-ticker.C:
			fmt.Fprintf(w, "data: ping\n\n")
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
		fmt.Println("ERROR: ", err)
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
