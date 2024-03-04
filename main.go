package main

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
)

func uploadHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Alleen POST is toegestaan", http.StatusMethodNotAllowed)
		return
	}

	// Zorg ervoor dat de map 'files' bestaat
	fileDir := "./files"
	if err := os.MkdirAll(fileDir, os.ModePerm); err != nil {
		http.Error(w, "Kon map niet maken", http.StatusInternalServerError)
		return
	}

	// Parse het multipart form (met een maximale uploadgrootte)
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

	// Gebruik de oorspronkelijke bestandsnaam uit de upload
	fileName := filepath.Base(fileHeader.Filename)
	filePath := filepath.Join(fileDir, fileName)

	// Bestand voor schrijven openen
	dst, err := os.Create(filePath)
	if err != nil {
		http.Error(w, "Fout bij het aanmaken van bestand", http.StatusInternalServerError)
		return
	}
	defer dst.Close()

	// Kopieer de inhoud van de geüploade file naar de bestemming
	if _, err := io.Copy(dst, file); err != nil {
		http.Error(w, "Fout bij het opslaan van bestand", http.StatusInternalServerError)
		return
	}

	w.Write([]byte("Bestand succesvol geüpload\n"))
}

func main() {

	// Serveer statische bestanden uit de ./ui map
	uiDir := "./ui"
	fs := http.FileServer(http.Dir(uiDir))
	http.Handle("/", http.StripPrefix("/", fs))

	http.HandleFunc("/upload", uploadHandler)

	println("Server start op :3000")
	if err := http.ListenAndServe(":3000", nil); err != nil {
		panic(err)
	}
}
