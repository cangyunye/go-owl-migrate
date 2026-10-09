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

// jobArtifactRoot is the per-job directory the master creates for every serve
// job (<tempDir>/<jobID>/). migrate exports + INSERT SQL live under it, and
// serve-launched export jobs write into its data/ subdir (see execSpawner).
func (s *Server) jobArtifactRoot(jobID string) string {
	return filepath.Join(s.tempDir, jobID)
}

// jobDataDirs lists the directories that may hold a job's data artifacts, most
// specific first: <root>/data (serve export jobs) then <root> (migrate exports
// land directly in the job dir). Missing dirs are skipped.
func (s *Server) jobDataDirs(jobID string) []string {
	root := s.jobArtifactRoot(jobID)
	var dirs []string
	if fi, err := os.Stat(filepath.Join(root, "data")); err == nil && fi.IsDir() {
		dirs = append(dirs, filepath.Join(root, "data"))
	}
	if fi, err := os.Stat(root); err == nil && fi.IsDir() {
		dirs = append(dirs, root)
	}
	return dirs
}

// isArtifactFile reports whether a regular file name is a downloadable data
// artifact. Internal job files (config.yaml, migrate_progress.json, …) are
// excluded by extension so they never surface as products.
func isArtifactFile(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".csv", ".tsv", ".xlsx", ".sql", ".txt":
		return true
	}
	return false
}

// artifactEntry is one artifact file belonging to a job.
type artifactEntry struct {
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	Modified string `json:"modified,omitempty"`
	Kind     string `json:"kind"` // data | sql
	Dir      string `json:"dir,omitempty"`
}

// collectJobDataFiles flattens a job's data artifacts across its data dirs,
// de-duplicating by file name (the most specific dir wins), newest first.
func (s *Server) collectJobDataFiles(jobID string) ([]artifactEntry, string) {
	seen := map[string]bool{}
	var files []artifactEntry
	primaryDir := ""
	for i, dir := range s.jobDataDirs(jobID) {
		if i == 0 {
			primaryDir = dir
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || seen[e.Name()] || !isArtifactFile(e.Name()) {
				continue
			}
			info, err := e.Info()
			if err != nil {
				continue
			}
			seen[e.Name()] = true
			files = append(files, artifactEntry{
				Name:     e.Name(),
				Size:     info.Size(),
				Modified: info.ModTime().Format(time.RFC3339),
				Kind:     "data",
				Dir:      dir,
			})
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Modified > files[j].Modified })
	return files, primaryDir
}

type exportFile struct {
	Name    string `json:"name"`
	Size    int64  `json:"size"`
	ModTime string `json:"modified"`
}

// handleJobExportFiles lists the DATA artifacts of one job only — never the
// shared export.output_dir, which would leak every other job's files into this
// job's detail page. 目录不存在返回空列表——任务可能尚未产出任何文件。
func (s *Server) handleJobExportFiles(w http.ResponseWriter, r *http.Request) {
	jobID := r.PathValue("id")
	if _, err := s.store.GetJob(jobID); err != nil {
		writeError(w, http.StatusNotFound, "job not found")
		return
	}
	files, dir := s.collectJobDataFiles(jobID)
	if files == nil {
		files = []artifactEntry{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"dir":   dir,
		"dirs":  s.jobDataDirs(jobID),
		"files": files,
	})
}

// handleJobExportFileDownload streams one data artifact from the job's own
// dirs. 路径清洗限制在产物目录内；仅允许已完成的任务下载，避免拉到写了一半
// 的文件。
func (s *Server) handleJobExportFileDownload(w http.ResponseWriter, r *http.Request) {
	jobID := r.PathValue("id")
	if job, err := s.store.GetJob(jobID); err != nil {
		writeError(w, http.StatusNotFound, "job not found")
		return
	} else if job.Status != "completed" && job.Status != "completed_with_errors" {
		writeError(w, http.StatusConflict, "job is not completed yet (status: "+job.Status+")")
		return
	}
	name := filepath.Clean("/" + r.URL.Query().Get("name")) // 强制相对化，防穿越
	full, ok := s.resolveJobArtifact(jobID, name)
	if !ok {
		writeError(w, http.StatusNotFound, "file not found")
		return
	}
	w.Header().Set("Content-Disposition", "attachment; filename*=UTF-8''"+url.PathEscape(filepath.Base(full)))
	http.ServeFile(w, r, full)
}

// resolveJobArtifact maps a client-supplied relative file name onto a real file
// inside one of the job's artifact dirs; anything escaping those dirs (or a
// directory itself) is rejected.
func (s *Server) resolveJobArtifact(jobID, name string) (string, bool) {
	for _, dir := range s.jobDataDirs(jobID) {
		full := filepath.Join(dir, filepath.FromSlash(name))
		cleanDir := filepath.Clean(dir)
		if !strings.HasPrefix(filepath.Clean(full), cleanDir+string(os.PathSeparator)) {
			continue
		}
		info, err := os.Stat(full)
		if err != nil || info.IsDir() {
			continue
		}
		return full, true
	}
	return "", false
}

// handleJobArtifactsDownload streams ALL artifacts of one job (data files +
// INSERT SQL under insert/) in the requested archive format. This is the
// single-job "打包下载" entry point.
func (s *Server) handleJobArtifactsDownload(w http.ResponseWriter, r *http.Request) {
	jobID := r.PathValue("id")
	if job, err := s.store.GetJob(jobID); err != nil {
		writeError(w, http.StatusNotFound, "job not found")
		return
	} else if job.Status != "completed" && job.Status != "completed_with_errors" {
		writeError(w, http.StatusConflict, "job is not completed yet (status: "+job.Status+")")
		return
	}
	entries := s.collectJobArchiveEntries(jobID)
	if len(entries) == 0 {
		writeError(w, http.StatusNotFound, "no artifacts for this job")
		return
	}
	format := r.URL.Query().Get("format")
	if format == "" {
		format = "tar.gz"
	}
	s.streamArtifactArchive(w, jobID, format, entries)
}

// archiveEntry is one file to include in a job bundle, with the name it should
// carry inside the archive.
type archiveEntry struct {
	ArchiveName string
	FullPath    string
}

// collectJobArchiveEntries gathers data + SQL artifacts for a job bundle. SQL
// files are namespaced under sql/ to avoid clashing with data file names.
func (s *Server) collectJobArchiveEntries(jobID string) []archiveEntry {
	var out []archiveEntry
	dataFiles, _ := s.collectJobDataFiles(jobID)
	for _, f := range dataFiles {
		full, ok := s.resolveJobArtifact(jobID, f.Name)
		if !ok {
			continue
		}
		out = append(out, archiveEntry{ArchiveName: f.Name, FullPath: full})
	}
	sqlDir := s.sqlOutputDir(jobID)
	if entries, err := os.ReadDir(sqlDir); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			out = append(out, archiveEntry{
				ArchiveName: "sql/" + e.Name(),
				FullPath:    filepath.Join(sqlDir, e.Name()),
			})
		}
	}
	return out
}

// streamArtifactArchive writes the entries as tar.gz or zip, streaming so large
// outputs are never buffered in memory.
func (s *Server) streamArtifactArchive(w http.ResponseWriter, jobID, format string, entries []archiveEntry) {
	switch format {
	case "tar.gz":
		w.Header().Set("Content-Type", "application/gzip")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s-artifacts.tar.gz"`, jobID))
		gz := gzip.NewWriter(w)
		defer gz.Close()
		tw := tar.NewWriter(gz)
		defer tw.Close()
		for _, e := range entries {
			fi, err := os.Stat(e.FullPath)
			if err != nil {
				continue
			}
			hdr, err := tar.FileInfoHeader(fi, "")
			if err != nil {
				continue
			}
			hdr.Name = e.ArchiveName
			if err := tw.WriteHeader(hdr); err != nil {
				return
			}
			f, err := os.Open(e.FullPath)
			if err != nil {
				continue
			}
			_, _ = io.Copy(tw, f)
			f.Close()
		}
	case "zip":
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s-artifacts.zip"`, jobID))
		zw := zip.NewWriter(w)
		defer zw.Close()
		for _, e := range entries {
			fi, err := os.Stat(e.FullPath)
			if err != nil {
				continue
			}
			hdr, err := zip.FileInfoHeader(fi)
			if err != nil {
				continue
			}
			hdr.Name = e.ArchiveName
			hdr.Method = zip.Deflate
			fw, err := zw.CreateHeader(hdr)
			if err != nil {
				return
			}
			f, err := os.Open(e.FullPath)
			if err != nil {
				continue
			}
			_, _ = io.Copy(fw, f)
			f.Close()
		}
	default:
		writeError(w, http.StatusBadRequest, "unsupported format: "+format)
	}
}

// sharedExportDir resolves the active config's export.output_dir (default
// ./output/data/). Files here belong to no individual job (legacy/standalone
// exports) — the artifact library lists them as "未归属任务".
func (s *Server) sharedExportDir() string {
	s.mu.RLock()
	cfg := s.cfg
	s.mu.RUnlock()
	dir := ""
	if cfg != nil {
		dir = cfg.Export.OutputDir
	}
	if strings.TrimSpace(dir) == "" {
		dir = "./output/data/"
	}
	return dir
}

// artifactJobSummary is one per-job bundle in the artifact library.
type artifactJobSummary struct {
	JobID     string `json:"job_id"`
	Type      string `json:"type"`
	Status    string `json:"status"`
	CreatedAt string `json:"created_at"`
	Finished  string `json:"finished_at,omitempty"`
	FileCount int    `json:"file_count"`
	TotalSize int64  `json:"total_size"`
	HasSQL    bool   `json:"has_sql"`
	Dir       string `json:"dir,omitempty"`
}

// handleListArtifacts lists every job that produced artifacts (each a
// single-job bundle keyed by job id), plus standalone files in the shared
// export dir that belong to no job.
func (s *Server) handleListArtifacts(w http.ResponseWriter, r *http.Request) {
	jobs, err := s.store.ListJobs(200)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	summaries := []artifactJobSummary{}
	for _, j := range jobs {
		files, dir := s.collectJobDataFiles(j.JobID)
		sqlFiles, _ := os.ReadDir(s.sqlOutputDir(j.JobID))
		sqlCount := 0
		for _, e := range sqlFiles {
			if !e.IsDir() {
				sqlCount++
			}
		}
		if len(files) == 0 && sqlCount == 0 {
			continue
		}
		var total int64
		for _, f := range files {
			total += f.Size
		}
		summaries = append(summaries, artifactJobSummary{
			JobID:     j.JobID,
			Type:      j.Type,
			Status:    j.Status,
			CreatedAt: j.CreatedAt,
			Finished:  j.FinishedAt,
			FileCount: len(files) + sqlCount,
			TotalSize: total,
			HasSQL:    sqlCount > 0,
			Dir:       dir,
		})
	}

	// Standalone: shared export.output_dir files with no job attribution.
	dir := s.sharedExportDir()
	standalone := []exportFile{}
	if entries, derr := os.ReadDir(dir); derr == nil {
		for _, e := range entries {
			if e.IsDir() || !isArtifactFile(e.Name()) {
				continue
			}
			info, ierr := e.Info()
			if ierr != nil {
				continue
			}
			standalone = append(standalone, exportFile{
				Name: e.Name(), Size: info.Size(), ModTime: info.ModTime().Format(time.RFC3339),
			})
		}
	}
	sort.Slice(standalone, func(i, j int) bool { return standalone[i].ModTime > standalone[j].ModTime })

	writeJSON(w, http.StatusOK, map[string]any{
		"jobs":       summaries,
		"standalone": map[string]any{"dir": dir, "files": standalone},
	})
}

// handleStandaloneArtifactDownload streams one file from the shared export dir,
// restricted to that directory (the only place unowned artifacts live).
func (s *Server) handleStandaloneArtifactDownload(w http.ResponseWriter, r *http.Request) {
	dir := s.sharedExportDir()
	name := filepath.Clean("/" + r.URL.Query().Get("name"))
	full := filepath.Join(dir, filepath.FromSlash(name))
	if !strings.HasPrefix(filepath.Clean(full), filepath.Clean(dir)+string(os.PathSeparator)) {
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
