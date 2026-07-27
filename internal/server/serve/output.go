package serve

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
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
	} else if job.Status != "completed" {
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
