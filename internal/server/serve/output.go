package serve

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/cangyunye/go-owl-migrate/internal/config"
)

// sqlOutputDir returns the directory where a sql-out migration worker writes
// its INSERT SQL files: <tempDir>/<jobID>/insert/.
func (s *Server) sqlOutputDir(jobID string) string {
	return filepath.Join(s.tempDir, jobID, "insert")
}

type outputFile struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
}

// handleJobOutput reports the SQL output of a sql-out migration job: whether
// any SQL was produced, and the file list with sizes.
func (s *Server) handleJobOutput(w http.ResponseWriter, r *http.Request) {
	jobID := r.PathValue("id")
	dir := s.sqlOutputDir(jobID)

	entries, err := os.ReadDir(dir)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"has_sql": false, "file_count": 0, "total_size": 0, "files": []outputFile{}})
		return
	}

	files := make([]outputFile, 0, len(entries))
	var total int64
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, outputFile{Name: e.Name(), Size: info.Size()})
		total += info.Size()
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Name < files[j].Name })

	writeJSON(w, http.StatusOK, map[string]any{
		"has_sql":    len(files) > 0,
		"dir":        dir,
		"file_count": len(files),
		"total_size": total,
		"files":      files,
	})
}

// handleJobOutputDownload streams the SQL output of a completed sql-out job in
// the requested archive format (tar.gz | zip | raw).
func (s *Server) handleJobOutputDownload(w http.ResponseWriter, r *http.Request) {
	jobID := r.PathValue("id")
	format := r.URL.Query().Get("format")
	if format == "" {
		format = "tar.gz"
	}

	// Only completed jobs may be downloaded, so users never pull a half-written
	// archive mid-migration.
	if job, err := s.store.GetJob(jobID); err != nil {
		writeError(w, http.StatusNotFound, "job not found")
		return
	} else if job.Status != "completed" && job.Status != "completed_with_errors" {
		writeError(w, http.StatusConflict, "job is not completed yet (status: "+job.Status+")")
		return
	}

	dir := s.sqlOutputDir(jobID)
	entries, err := os.ReadDir(dir)
	if err != nil {
		writeError(w, http.StatusNotFound, "no SQL output for this job")
		return
	}
	var files []os.DirEntry
	for _, e := range entries {
		if !e.IsDir() {
			files = append(files, e)
		}
	}
	if len(files) == 0 {
		writeError(w, http.StatusNotFound, "no SQL output for this job")
		return
	}

	switch format {
	case "tar.gz":
		s.streamTarGz(w, jobID, dir, files)
	case "zip":
		s.streamZip(w, jobID, dir, files)
	case "raw":
		if len(files) != 1 {
			writeError(w, http.StatusBadRequest, "raw format requires exactly one SQL file; use tar.gz or zip for multiple files")
			return
		}
		s.streamRaw(w, dir, files[0])
	default:
		writeError(w, http.StatusBadRequest, "unsupported format: "+format)
	}
}

// streamTarGz writes the SQL files as a gzipped tar archive, streaming to the
// response so large outputs are not buffered in memory.
func (s *Server) streamTarGz(w http.ResponseWriter, jobID, dir string, files []os.DirEntry) {
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s-sql.tar.gz"`, jobID))

	gz := gzip.NewWriter(w)
	defer gz.Close()
	tw := tar.NewWriter(gz)
	defer tw.Close()

	for _, e := range files {
		info, err := e.Info()
		if err != nil {
			continue
		}
		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			continue
		}
		hdr.Name = e.Name()
		if err := tw.WriteHeader(hdr); err != nil {
			return
		}
		f, err := os.Open(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		_, _ = io.Copy(tw, f)
		f.Close()
	}
}

// streamZip writes the SQL files as a zip archive, streaming to the response.
func (s *Server) streamZip(w http.ResponseWriter, jobID, dir string, files []os.DirEntry) {
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s-sql.zip"`, jobID))

	zw := zip.NewWriter(w)
	defer zw.Close()

	for _, e := range files {
		info, err := e.Info()
		if err != nil {
			continue
		}
		hdr, err := zip.FileInfoHeader(info)
		if err != nil {
			continue
		}
		hdr.Name = e.Name()
		hdr.Method = zip.Deflate
		fw, err := zw.CreateHeader(hdr)
		if err != nil {
			return
		}
		f, err := os.Open(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		_, _ = io.Copy(fw, f)
		f.Close()
	}
}

// streamRaw serves a single SQL file directly (no archive).
func (s *Server) streamRaw(w http.ResponseWriter, dir string, e os.DirEntry) {
	f, err := os.Open(filepath.Join(dir, e.Name()))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "open file: "+err.Error())
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "application/sql")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, e.Name()))
	_, _ = io.Copy(w, f)
}

// jobExportOutputDir resolves the data-export output directory of a job:
// export.output_dir from the job's stored config (default ./output/data/).
// Data-export and migrate jobs write their CSV/xlsx there (shared dir), unlike
// sql-out jobs whose INSERT files land in the per-job insert dir above.
func (s *Server) jobExportOutputDir(jobID string) (string, error) {
	job, err := s.store.GetJob(jobID)
	if err != nil {
		return "", err
	}
	var cfg config.Config
	if err := yaml.Unmarshal([]byte(job.Config), &cfg); err != nil {
		return "", err
	}
	dir := cfg.Export.OutputDir
	if dir == "" {
		dir = "./output/data/"
	}
	return dir, nil
}

type exportFile struct {
	Name    string `json:"name"`
	Size    int64  `json:"size"`
	ModTime string `json:"modified"`
}

// handleJobExportFiles lists the data-export artifacts of a job (flat listing
// of the job's export output dir, newest first). 目录不存在返回空列表——
// 任务可能尚未产出任何文件。
func (s *Server) handleJobExportFiles(w http.ResponseWriter, r *http.Request) {
	jobID := r.PathValue("id")
	dir, err := s.jobExportOutputDir(jobID)
	if err != nil {
		writeError(w, http.StatusNotFound, "job not found")
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"dir": dir, "files": []exportFile{}})
		return
	}
	files := make([]exportFile, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, exportFile{
			Name:    e.Name(),
			Size:    info.Size(),
			ModTime: info.ModTime().Format(time.RFC3339),
		})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].ModTime > files[j].ModTime })
	writeJSON(w, http.StatusOK, map[string]any{"dir": dir, "files": files})
}

// handleJobExportFileDownload streams one exported artifact. 路径清洗限制在
// 产物目录内；仅允许已完成的任务下载，避免拉到写了一半的文件。
func (s *Server) handleJobExportFileDownload(w http.ResponseWriter, r *http.Request) {
	jobID := r.PathValue("id")
	if job, err := s.store.GetJob(jobID); err != nil {
		writeError(w, http.StatusNotFound, "job not found")
		return
	} else if job.Status != "completed" && job.Status != "completed_with_errors" {
		writeError(w, http.StatusConflict, "job is not completed yet (status: "+job.Status+")")
		return
	}
	dir, err := s.jobExportOutputDir(jobID)
	if err != nil {
		writeError(w, http.StatusNotFound, "job not found")
		return
	}
	name := filepath.Clean("/" + r.URL.Query().Get("name")) // 强制相对化，防穿越
	full := filepath.Join(dir, filepath.FromSlash(name))
	cleanDir := filepath.Clean(dir)
	if !strings.HasPrefix(full, cleanDir+string(os.PathSeparator)) {
		writeError(w, http.StatusBadRequest, "invalid file name")
		return
	}
	info, err := os.Stat(full)
	if err != nil || info.IsDir() {
		writeError(w, http.StatusNotFound, "file not found")
		return
	}
	w.Header().Set("Content-Disposition", "attachment; filename*=UTF-8''"+url.PathEscape(filepath.Base(full)))
	http.ServeFile(w, r, full)
}
