package fs

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// maxReadSize limits file reads to 10 MB to prevent memory exhaustion.
const maxReadSize = 10 << 20

type FileInfo struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	IsDir   bool   `json:"is_dir"`
	Size    int64  `json:"size"`
	ModTime string `json:"mod_time"`
}

// ListDirectory returns contents of a given path
func ListDirectory(dirPath string) ([]FileInfo, error) {
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		return nil, err
	}

	var results []FileInfo
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			continue // skip unreadable files
		}
		results = append(results, FileInfo{
			Name:    e.Name(),
			Path:    filepath.Join(dirPath, e.Name()),
			IsDir:   e.IsDir(),
			Size:    info.Size(),
			ModTime: info.ModTime().Format("2006-01-02 15:04:05"),
		})
	}
	return results, nil
}

// ReadFileContent returns the text content of a file (capped at maxReadSize).
func ReadFileContent(filePath string) (string, error) {
	info, err := os.Stat(filePath)
	if err != nil {
		return "", err
	}
	if info.Size() > maxReadSize {
		return "", fmt.Errorf("file too large (%d bytes, max %d)", info.Size(), maxReadSize)
	}
	b, err := os.ReadFile(filePath)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// WriteFileContent writes text content to a file, overwriting if exists
func WriteFileContent(filePath string, content string) error {
	f, err := os.OpenFile(filePath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = io.WriteString(f, content)
	return err
}
