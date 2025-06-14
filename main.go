package main

import (
	"archive/zip"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/gorilla/mux"
)

const StorageRoot = "./mock-zfs-storage" // Update to "/codebases" in production

func main() {
	r := mux.NewRouter()

	r.HandleFunc("/upload/{uuid}", UploadHandler).Methods("POST")
	r.HandleFunc("/download/{uuid}/{filepath:.*}", DownloadFileHandler).Methods("GET")
	r.HandleFunc("/download-zip/{uuid}", DownloadZipHandler).Methods("GET")

	log.Println("Server B running on port 8081...")
	http.ListenAndServe(":8081", r)
}

func UploadHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	uuid := vars["uuid"]
	basePath := filepath.Join(StorageRoot, uuid)
	os.MkdirAll(basePath, os.ModePerm)

	err := r.ParseMultipartForm(100 << 20) // 100MB max
	if err != nil {
		http.Error(w, "Could not parse form", http.StatusBadRequest)
		return
	}

	for _, headers := range r.MultipartForm.File {
		for _, header := range headers {
			file, err := header.Open()
			if err != nil {
				http.Error(w, "Could not open uploaded file", http.StatusInternalServerError)
				return
			}
			defer file.Close()

			targetPath := filepath.Join(basePath, header.Filename)
			os.MkdirAll(filepath.Dir(targetPath), os.ModePerm)

			out, err := os.Create(targetPath)
			if err != nil {
				http.Error(w, "Could not save file", http.StatusInternalServerError)
				return
			}
			defer out.Close()

			io.Copy(out, file)
		}
	}

	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, "Upload successful")
}

func DownloadFileHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	uuid := vars["uuid"]
	filePath := vars["filepath"]

	fullPath := filepath.Join(StorageRoot, uuid, filePath)

	file, err := os.Open(fullPath)
	if err != nil {
		http.Error(w, "File not found", http.StatusNotFound)
		return
	}
	defer file.Close()

	w.Header().Set("Content-Disposition", "attachment; filename="+filepath.Base(fullPath))
	w.Header().Set("Content-Type", "application/octet-stream")
	io.Copy(w, file)
}

func DownloadZipHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	uuid := vars["uuid"]
	basePath := filepath.Join(StorageRoot, uuid)

	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", "attachment; filename="+uuid+".zip")

	zipWriter := zip.NewWriter(w)
	defer zipWriter.Close()

	filepath.Walk(basePath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		relPath := strings.TrimPrefix(path, basePath+string(filepath.Separator))
		zipFile, err := zipWriter.Create(relPath)
		if err != nil {
			return err
		}
		fsFile, err := os.Open(path)
		if err != nil {
			return err
		}
		defer fsFile.Close()
		_, err = io.Copy(zipFile, fsFile)
		return err
	})
}
