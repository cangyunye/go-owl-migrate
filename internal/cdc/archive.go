package cdc

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// AckedFromDone derives per-table acked chg ids from a done/ directory. For each
// table, it finds the highest seq with a contiguous run starting at 1 and uses
// that file's EndChgID as the acked watermark. Files out of order (gaps) do not
// advance the acked watermark beyond the contiguous prefix.
func AckedFromDone(dir string) (map[string]int64, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]int64{}, nil
		}
		return nil, err
	}
	// Group by table: map[table]map[seq]endChgID and map[table]maxSeq
	byTable := map[string]map[int64]int64{}
	for _, e := range entries {
		bf, perr := ParseBatchFileName(e.Name())
		if perr != nil {
			continue
		}
		m, ok := byTable[bf.Table]
		if !ok {
			m = map[int64]int64{}
			byTable[bf.Table] = m
		}
		m[bf.Seq] = bf.EndChgID
	}
	acked := map[string]int64{}
	for tbl, seqs := range byTable {
		// contiguous prefix starting at seq 1
		highest := int64(0)
		for s := int64(1); ; s++ {
			endID, ok := seqs[s]
			if !ok {
				break
			}
			highest = endID
		}
		acked[tbl] = highest
	}
	return acked, nil
}

// ArchiveDone compresses executed batch files from done/ into per-(table,date)
// tar.gz archives under archiveDir, then removes the originals. Returns the
// number of archived files. Follows the archive config contract (default
// tar.gz, table/date organized).
func ArchiveDone(doneDir, archiveDir string) (int, error) {
	entries, err := os.ReadDir(doneDir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}

	// Group done files by [table][date] → []path
	groups := map[string]map[string][]string{}
	var order []string // "table|date"
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".sql" {
			continue
		}
		bf, perr := ParseBatchFileName(e.Name())
		if perr != nil {
			continue
		}
		date := bf.Start.Format("20060102")
		g, ok := groups[bf.Table]
		if !ok {
			g = map[string][]string{}
			groups[bf.Table] = g
		}
		g[date] = append(g[date], filepath.Join(doneDir, e.Name()))
		key := bf.Table + "|" + date
		order = append(order, key)
	}
	// unique keys in stable order
	seen := map[string]bool{}
	var keys []string
	for _, k := range order {
		if !seen[k] {
			seen[k] = true
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)

	count := 0
	for _, key := range keys {
		parts := strings.SplitN(key, "|", 2)
		table, date := parts[0], parts[1]
		files := groups[table][date]
		tarPath := filepath.Join(archiveDir, table, date+".tar.gz")
		if err := os.MkdirAll(filepath.Dir(tarPath), 0755); err != nil {
			return count, err
		}
		if err := tarGzFiles(tarPath, files); err != nil {
			return count, fmt.Errorf("archive %s: %w", tarPath, err)
		}
		for _, f := range files {
			os.Remove(f)
		}
		count += len(files)
	}
	return count, nil
}

func tarGzFiles(tarPath string, files []string) error {
	out, err := os.Create(tarPath)
	if err != nil {
		return err
	}
	defer out.Close()
	gz := gzip.NewWriter(out)
	defer gz.Close()
	tw := tar.NewWriter(gz)
	defer tw.Close()

	for _, f := range files {
		info, err := os.Lstat(f)
		if err != nil {
			return err
		}
		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		hdr.Name = filepath.Base(f)
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		src, err := os.Open(f)
		if err != nil {
			return err
		}
		if _, err := io.Copy(tw, src); err != nil {
			src.Close()
			return err
		}
		src.Close()
	}
	return nil
}
