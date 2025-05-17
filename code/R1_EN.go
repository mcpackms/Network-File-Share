// File server with directory browsing and file download capabilities
// Features:
// - Cross-platform path handling
// - Secure path validation
// - Directory listing with styled HTML
// - File download support with proper MIME types
// - Interactive directory input
// - Network IP detection
package main

import (
	"bufio"
	"flag"
	"fmt"
	"html"       // HTML escaping for XSS prevention
	"html/template"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath" // Cross-platform path manipulation
	"strconv"
	"strings"
	"time"
)

// Global configuration variables
var (
	rootDir string // Absolute path of shared directory
	port    string // Listening port number
)

// HTML template for directory listing
// Security features:
// - Automatic HTML escaping
// - URL path encoding
var dirListTemplate = template.Must(template.New("").Parse(`
<html>
<head>
    <meta charset="UTF-8">
    <title>File Server - {{.RelPath}}</title>
    <style>
        li { font-family: monospace; }
        dir { color: blue; }  /* Directory style */
        file { color: green; } /* File style */
    </style>
</head>
<body>
    <h1>Directory Listing: {{.RelPath}}</h1>
    <ul>
        {{if .HasParent}}<li><a href="{{.ParentPath}}">.. (Parent Directory)</a></li>{{end}}
        {{range .Files}}
            <li><a href="{{.URL}}">{{if .IsDir}}<dir>{{.Name}}</dir>{{else}}<file>{{.Name}}</file>{{end}}</a></li>
        {{end}}
    </ul>
</body>
</html>
`))

// Initialize command-line flags
func init() {
	flag.StringVar(&rootDir, "dir", "", "Directory to share")
	flag.StringVar(&rootDir, "directory", "", "Alias for --dir")
	flag.StringVar(&port, "port", "8080", "HTTP server port")
}

// Main entry point
func main() {
	flag.Parse()

	// Get local IP for user feedback
	localIP := getLocalIP()

	// Interactive directory input
	if rootDir == "" {
		reader := bufio.NewReader(os.Stdin)
		for { // Input validation loop
			log.Print("Enter directory path to share (e.g. /sdcard or C:\\): ")
			input, _ := reader.ReadString('\n')
			rootDir = strings.TrimSpace(input)
			if err := validateDirectory(rootDir); err == nil {
				break
			}
			log.Printf("Invalid path: %v, please retry", err)
		}
	}

	// Resolve symbolic links
	resolvedRoot, err := filepath.EvalSymlinks(rootDir)
	if err != nil {
		log.Fatalf("Symbolic link resolution failed: %v", err)
	}
	rootDir = resolvedRoot // Update to real path

	// Configure logging
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	
	// Startup information
	log.Printf("\n[START] File Server Configuration\n"+
		"  Shared Directory: %s\n"+
		"  Listening Port: %s\n"+
		"  Local Access: http://127.0.0.1:%s\n"+
		"  Network Access: http://%s:%s\n"+
		"  Press CTRL+C to exit",
		rootDir, port, port, localIP, port)

	// Configure HTTP server
	server := &http.Server{
		Addr:         ":" + port,
		ReadTimeout:  10 * time.Second,  // Prevent slowloris attacks
		WriteTimeout: 30 * time.Second,  // File transfer timeout
	}

	// Root handler
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		startTime := time.Now()
		reqPath := r.URL.Path
		log.Printf("[REQUEST] %s %s", r.Method, reqPath)

		// Request completion logging
		defer func() {
			log.Printf("[COMPLETE] %s %s Duration: %v", 
				r.Method, reqPath, time.Since(startTime))
		}()

		// Path sanitization
		path := strings.TrimPrefix(reqPath, "/")
		cleanedPath := filepath.Clean(path) // Prevent path traversal
		fullPath := filepath.Join(rootDir, cleanedPath)

		// Debug path resolution
		log.Printf("Processing path: %s → %s", reqPath, fullPath)

		// Open target path
		file, err := os.Open(fullPath)
		if err != nil {
			log.Printf("File open failed: %v", err)
			http.Error(w, "404 Not Found", http.StatusNotFound)
			return
		}
		defer file.Close()

		// Get file metadata
		fileInfo, err := file.Stat()
		if err != nil {
			log.Printf("File stat failed: %v", err)
			http.Error(w, "500 Internal Server Error", http.StatusInternalServerError)
			return
		}

		// Handle directory vs file
		if fileInfo.IsDir() {
			listDir(w, r, fullPath, cleanedPath)
		} else {
			sendFile(w, r, fullPath, fileInfo.Name(), fileInfo.Size())
		}
	})

	// Start server
	if err := server.ListenAndServe(); err != nil {
		log.Fatalf("Server startup failed: %v (Possible causes: port in use or permission denied)", err)
	}
}

// Validate directory existence and permissions
func validateDirectory(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("path access error: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("not a directory")
	}
	return nil
}

// Generate directory listing page
func listDir(w http.ResponseWriter, r *http.Request, dirPath string, relPath string) {
	// Read directory contents
	files, err := os.ReadDir(dirPath)
	if err != nil {
		log.Printf("Directory read error: %v", err)
		http.Error(w, "403 Forbidden", http.StatusForbidden)
		return
	}

	// Template data structure
	type FileInfo struct {
		URL   string // URL-encoded path
		Name  string // HTML-escaped name
		IsDir bool   // Directory flag
	}
	data := struct {
		RelPath    string    // Display path
		ParentPath string    // URL-encoded parent path
		HasParent  bool      // Has parent directory
		Files      []FileInfo
	}{
		RelPath:    html.EscapeString(relPath),
		HasParent:  relPath != "",
		ParentPath: url.PathEscape(filepath.ToSlash(filepath.Dir(relPath))),
	}

	// Build file list
	for _, file := range files {
		name := file.Name()
		urlPath := url.PathEscape(filepath.ToSlash(filepath.Join(relPath, name)))
		data.Files = append(data.Files, FileInfo{
			URL:   urlPath,
			Name:  html.EscapeString(name),
			IsDir: file.IsDir(),
		})
	}

	// Render template
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := dirListTemplate.Execute(w, data); err != nil {
		log.Printf("Template rendering error: %v", err)
	}
}

// Handle file download with proper headers
func sendFile(w http.ResponseWriter, r *http.Request, filePath, fileName string, fileSize int64) {
	file, err := os.Open(filePath)
	if err != nil {
		log.Printf("File open error: %v", err)
		http.Error(w, "404 Not Found", http.StatusNotFound)
		return
	}
	defer file.Close()

	// Set download headers
	encodedName := url.PathEscape(fileName)
	w.Header().Set("Content-Disposition", 
		fmt.Sprintf(`attachment; filename="%s"; filename*=UTF-8''%s`, 
			encodedName, encodedName))
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.FormatInt(fileSize, 10))

	// Stream file content
	if _, err := io.Copy(w, file); err != nil {
		log.Printf("File transfer error: %v", err)
	}
}

// Detect local non-loopback IP address
func getLocalIP() string {
	// Method 1: Get outgoing IP via UDP connection
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err == nil {
		defer conn.Close()
		return strings.Split(conn.LocalAddr().String(), ":")[0]
	}

	// Method 2: Fallback to interface scanning
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return "127.0.0.1"
	}

	for _, addr := range addrs {
		if ipNet, ok := addr.(*net.IPNet); ok && !ipNet.IP.IsLoopback() {
			if ipNet.IP.To4() != nil {
				return ipNet.IP.String()
			}
		}
	}
	return "127.0.0.1"
}
