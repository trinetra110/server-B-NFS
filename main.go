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
	// Ensure storage directory exists
	if err := os.MkdirAll(StorageRoot, os.ModePerm); err != nil {
		log.Fatalf("Failed to create storage directory: %v", err)
	}

	r := mux.NewRouter()

	// API routes
	r.HandleFunc("/upload/{uuid}", UploadHandler).Methods("POST")
	r.HandleFunc("/download/{uuid}/{filepath:.*}", DownloadFileHandler).Methods("GET")
	r.HandleFunc("/download-zip/{uuid}", DownloadZipHandler).Methods("GET")
	r.HandleFunc("/health", HealthCheckHandler).Methods("GET")

	// Apply CORS middleware
	handler := enableCORS(r)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8081"
	}

	log.Printf("Server B running on port %s...", port)
	log.Printf("Storage root: %s", StorageRoot)
	log.Fatal(http.ListenAndServe(":"+port, handler))
}

func HealthCheckHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, `{"status": "ok", "storage_root": "`+StorageRoot+`"}`)
}

func UploadHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	uuid := vars["uuid"]
	basePath := filepath.Join(StorageRoot, uuid)

	// Create directory structure
	if err := os.MkdirAll(basePath, os.ModePerm); err != nil {
		log.Printf("Failed to create directory %s: %v", basePath, err)
		http.Error(w, "Could not create storage directory", http.StatusInternalServerError)
		return
	}

	// Parse multipart form with increased memory limit
	err := r.ParseMultipartForm(100 << 20) // 100MB max
	if err != nil {
		log.Printf("Could not parse multipart form: %v", err)
		http.Error(w, "Could not parse form", http.StatusBadRequest)
		return
	}

	if r.MultipartForm == nil || r.MultipartForm.File == nil {
		http.Error(w, "No files in request", http.StatusBadRequest)
		return
	}

	filesProcessed := 0
	for fieldName, headers := range r.MultipartForm.File {
		log.Printf("Processing field: %s with %d files", fieldName, len(headers))

		for _, header := range headers {
			file, err := header.Open()
			if err != nil {
				log.Printf("Could not open uploaded file %s: %v", header.Filename, err)
				continue
			}

			// Create target path, ensuring directory structure
			targetPath := filepath.Join(basePath, header.Filename)
			targetDir := filepath.Dir(targetPath)

			if err := os.MkdirAll(targetDir, os.ModePerm); err != nil {
				log.Printf("Could not create directory %s: %v", targetDir, err)
				file.Close()
				continue
			}

			out, err := os.Create(targetPath)
			if err != nil {
				log.Printf("Could not create file %s: %v", targetPath, err)
				file.Close()
				continue
			}

			written, err := io.Copy(out, file)
			out.Close()
			file.Close()

			if err != nil {
				log.Printf("Could not copy file %s: %v", header.Filename, err)
				os.Remove(targetPath) // Clean up partial file
				continue
			}

			log.Printf("Successfully saved file: %s (%d bytes)", targetPath, written)
			filesProcessed++
		}
	}

	if filesProcessed == 0 {
		http.Error(w, "No files were successfully processed", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, "Upload successful: %d files processed", filesProcessed)
}

func DownloadFileHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	uuid := vars["uuid"]
	filePath := vars["filepath"]

	// Security check: prevent path traversal
	if strings.Contains(filePath, "..") {
		http.Error(w, "Invalid file path", http.StatusBadRequest)
		return
	}

	fullPath := filepath.Join(StorageRoot, uuid, filePath)

	// Check if file exists and is actually a file
	fileInfo, err := os.Stat(fullPath)
	if err != nil {
		if os.IsNotExist(err) {
			http.Error(w, "File not found", http.StatusNotFound)
		} else {
			http.Error(w, "Error accessing file", http.StatusInternalServerError)
		}
		return
	}

	if fileInfo.IsDir() {
		http.Error(w, "Path is a directory, not a file", http.StatusBadRequest)
		return
	}

	file, err := os.Open(fullPath)
	if err != nil {
		log.Printf("Error opening file %s: %v", fullPath, err)
		http.Error(w, "Could not open file", http.StatusInternalServerError)
		return
	}
	defer file.Close()

	// Set appropriate headers
	w.Header().Set("Content-Disposition", "attachment; filename="+filepath.Base(filePath))
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", fileInfo.Size()))

	// Copy file content to response
	_, err = io.Copy(w, file)
	if err != nil {
		log.Printf("Error copying file content: %v", err)
	}
}

func DownloadZipHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	uuid := vars["uuid"]
	basePath := filepath.Join(StorageRoot, uuid)

	// Check if directory exists
	if _, err := os.Stat(basePath); os.IsNotExist(err) {
		http.Error(w, "Codebase not found", http.StatusNotFound)
		return
	}

	// Set headers for ZIP download
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", "attachment; filename="+uuid+".zip")

	// Create ZIP writer
	zipWriter := zip.NewWriter(w)
	defer zipWriter.Close()

	// Walk through the directory and add files to ZIP
	err := filepath.Walk(basePath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			log.Printf("Error walking path %s: %v", path, err)
			return err
		}

		// Skip directories
		if info.IsDir() {
			return nil
		}

		// Get relative path from base
		relPath, err := filepath.Rel(basePath, path)
		if err != nil {
			log.Printf("Error getting relative path: %v", err)
			return err
		}

		// Normalize path separators for ZIP (always use forward slashes)
		relPath = strings.ReplaceAll(relPath, "\\", "/")

		// Create file in ZIP
		zipFile, err := zipWriter.Create(relPath)
		if err != nil {
			log.Printf("Error creating ZIP entry for %s: %v", relPath, err)
			return err
		}

		// Open source file
		fsFile, err := os.Open(path)
		if err != nil {
			log.Printf("Error opening file %s: %v", path, err)
			return err
		}
		defer fsFile.Close()

		// Copy file content to ZIP entry
		_, err = io.Copy(zipFile, fsFile)
		if err != nil {
			log.Printf("Error copying file %s to ZIP: %v", path, err)
		}

		return err
	})

	if err != nil {
		log.Printf("Error creating ZIP: %v", err)
		// Note: At this point, we've already started writing the response,
		// so we can't return an HTTP error status
	}
}

func enableCORS(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}

		h.ServeHTTP(w, r)
	})
}
