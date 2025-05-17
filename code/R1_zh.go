package main

import (
	"bufio"
	"flag"
	"fmt"
	"html"
	"html/template"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

var (
	rootDir string
	port    string
)

var dirListTemplate = template.Must(template.New("").Parse(`
<!-- HarmonyOS Sans 字体 -->
<link href="https://cdn.jsdelivr.net/npm/harmonyos_sans_web@latest/css/harmonyos_sans.css" rel="stylesheet">
<!-- 思源黑体 -->
<link href="https://fonts.loli.net/css?family=Noto+Sans+SC" rel="stylesheet">

<html>
<head>
    <meta charset="UTF-8">
    <title>文件服务 - {{.RelPath}}</title>
    <style>
        /* 免费商用字体配置 */
        li { 
            font-family: "HarmonyOS Sans", "思源黑体", sans-serif;
            font-size:14px; 
            line-height:1.8;
        }
        dir { color: #2196F3; }
        file { color: #4CAF50; }
    </style>
</head>
<body>
    <h2>📂 当前目录：{{.RelPath}}</h2>
    <ul>
        {{if .HasParent}}<li><a href="{{.ParentPath}}">↑ 返回上级</a></li>{{end}}
        {{range .Files}}
            <li><a href="{{.URL}}">
                {{if .IsDir}}<dir>📁 {{.Name}}</dir>
                {{else}}<file>📄 {{.Name}}</file>{{end}}
            </a></li>
        {{end}}
    </ul>
</body>
</html>
`))


func init() {
	flag.StringVar(&rootDir, "dir", "", "指定共享目录")
	flag.StringVar(&rootDir, "directory", "", "同上")
	flag.StringVar(&port, "port", "8080", "监听端口")
}

func main() {
	flag.Parse()

	localIP := getLocalIP()

	if rootDir == "" {
		reader := bufio.NewReader(os.Stdin)
		for {
			log.Print("请输入要共享的目录路径:  如/sdcard或/root")
			input, _ := reader.ReadString('\n')
			rootDir = strings.TrimSpace(input)
			var err error
			if err = validateDirectory(rootDir); err == nil {
				break
			}
			log.Printf("路径无效: %v，请重新输入", err)
		}
	}

	resolvedRoot, err := filepath.EvalSymlinks(rootDir)
	if err != nil {
		log.Fatalf("解析符号链接失败: %v", err)
	}
	rootDir = resolvedRoot

	log.SetFlags(log.LstdFlags | log.Lshortfile)
	log.Printf("\n[start]文件服务器配置\n"+
		"  共享目录: %s\n"+
		"  监听端口: %s\n"+
		"  本地访问: http://127.0.0.1:%s\n"+
		"  局域网访问: http://%s:%s\n"+
		"  ctrl+c退出",
		rootDir, port, port, localIP, port)

	server := &http.Server{
		Addr:         ":" + port,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
	}

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		startTime := time.Now()
		reqPath := r.URL.Path
		log.Printf("[request]%s %s", r.Method, reqPath)

		defer func() {
			log.Printf("[finish]%s %s 耗时: %v", r.Method, reqPath, time.Since(startTime))
		}()

		path := strings.TrimPrefix(reqPath, "/")
		cleanedPath := filepath.Clean(path)
		fullPath := filepath.Join(rootDir, cleanedPath)

		// 调试日志：打印处理后的路径
		log.Printf("处理路径: %s → %s", reqPath, fullPath)

		file, err := os.Open(fullPath)
		if err != nil {
			log.Printf("打开文件失败: %v", err)
			http.Error(w, "文件未找到", http.StatusNotFound)
			return
		}
		defer file.Close()

		fileInfo, err := file.Stat()
		if err != nil {
			log.Printf("获取文件信息失败: %v", err)
			http.Error(w, "文件访问错误", http.StatusInternalServerError)
			return
		}

		if fileInfo.IsDir() {
			listDir(w, r, fullPath, cleanedPath)
		} else {
			sendFile(w, r, fullPath, fileInfo.Name(), fileInfo.Size())
		}
	})

	if err := server.ListenAndServe(); err != nil {
		log.Fatalf("启动失败: %v (可能原因：端口被占用或权限不足，建议改高端口，如8082)", err)
	}
}

func validateDirectory(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return os.ErrInvalid
	}
	return nil
}

func listDir(w http.ResponseWriter, r *http.Request, dirPath string, relPath string) {
	files, err := os.ReadDir(dirPath)
	if err != nil {
		log.Printf("读取目录失败: %v", err)
		http.Error(w, "目录不可读", http.StatusInternalServerError)
		return
	}

	type FileInfo struct {
		URL   string
		Name  string
		IsDir bool
	}
	data := struct {
		RelPath    string
		ParentPath string
		HasParent  bool
		Files      []FileInfo
	}{
		RelPath:    html.EscapeString(relPath),
		HasParent:  relPath != "",
		ParentPath: url.PathEscape(filepath.ToSlash(filepath.Dir(relPath))), // 父路径URL编码
	}

	for _, file := range files {
		name := file.Name()
		// 对文件名进行URL编码，但显示时保持原样
		urlPath := url.PathEscape(filepath.ToSlash(filepath.Join(relPath, name)))
		data.Files = append(data.Files, FileInfo{
			URL:   urlPath, // 直接使用编码后的URL
			Name:  html.EscapeString(name), // 显示时转义HTML
			IsDir: file.IsDir(),
		})
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := dirListTemplate.Execute(w, data); err != nil {
		log.Printf("模板渲染失败: %v", err)
	}
}

func sendFile(w http.ResponseWriter, r *http.Request, filePath, fileName string, fileSize int64) {
	file, err := os.Open(filePath)
	if err != nil {
		log.Printf("打开文件失败: %v", err)
		http.Error(w, "文件未找到", http.StatusNotFound)
		return
	}
	defer file.Close()

	// 兼容各种浏览器的文件名编码方式
	encodedName := url.PathEscape(fileName)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"; filename*=UTF-8''%s`, encodedName, encodedName))
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.FormatInt(fileSize, 10))

	if _, err := io.Copy(w, file); err != nil {
		log.Printf("文件传输失败: %v", err)
	}
}

func getLocalIP() string {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err == nil {
		defer conn.Close()
		return strings.Split(conn.LocalAddr().String(), ":")[0]
	}

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
