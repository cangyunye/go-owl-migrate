package cdc

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"
)

// BatchFile is a parsed batch file name plus its metadata.
type BatchFile struct {
	Seq        int64
	Start      time.Time
	End        time.Time
	Table      string
	StartChgID int64
	EndChgID   int64
	IsTruncate bool
	FileName   string
}

// batchNameRe matches {seq}_{start}_{end}_{table}_{startChg}-{endChg}.sql
// Table names are raw (sanitized of path separators only). Names that exceed
// the seq/date widths are not produced by this tool.
var batchNameRe = regexp.MustCompile(`^(\d{6})_(\d{14})-(\d{14})_([^_]+)_(\d+)-(\d+)(?:_T)?\.sql$`)

const batchTimeLayout = "20060102150405"

// BatchFileName builds a batch file name.
func BatchFileName(seq int64, start, end time.Time, table string, startChgID, endChgID int64, isTruncate bool) string {
	suffix := ""
	if isTruncate {
		suffix = "_T"
	}
	return fmt.Sprintf("%06d_%s-%s_%s_%d-%d%s.sql",
		seq,
		start.Format(batchTimeLayout),
		end.Format(batchTimeLayout),
		sanitizeTable(table),
		startChgID, endChgID,
		suffix,
	)
}

// sanitizeTable replaces os path separators so a table name never escapes the
// batch directory or creates subdirectories.
func sanitizeTable(name string) string {
	name = strings.ReplaceAll(name, "/", "_")
	name = strings.ReplaceAll(name, string(os.PathSeparator), "_")
	return name
}

// ParseBatchFileName parses a batch file name into its metadata.
func ParseBatchFileName(fileName string) (*BatchFile, error) {
	m := batchNameRe.FindStringSubmatch(fileName)
	if m == nil {
		return nil, fmt.Errorf("not a batch file name: %q", fileName)
	}
	start, err := time.Parse(batchTimeLayout, m[2])
	if err != nil {
		return nil, fmt.Errorf("parse start time: %w", err)
	}
	end, err := time.Parse(batchTimeLayout, m[3])
	if err != nil {
		return nil, fmt.Errorf("parse end time: %w", err)
	}
	var seq, sc, ec int64
	fmt.Sscanf(m[1], "%d", &seq)
	fmt.Sscanf(m[5], "%d", &sc)
	fmt.Sscanf(m[6], "%d", &ec)
	return &BatchFile{
		Seq:        seq,
		Start:      start,
		End:        end,
		Table:      m[4],
		StartChgID: sc,
		EndChgID:   ec,
		IsTruncate: strings.HasSuffix(fileName, "_T.sql"),
		FileName:   fileName,
	}, nil
}

// PendingBatch is a fully-written batch file waiting for execution.
type PendingBatch struct {
	Dir      string // pending directory
	FileName string
	Path     string
	Seq      int64
}

// NextSeqFromPending returns the next sequence number by scanning an existing
// pending/done directory for the highest seq, plus 1. A fresh directory yields 1.
func NextSeqFromPending(dir string) (int64, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 1, nil
		}
		return 0, err
	}
	var max int64
	for _, e := range entries {
		bf, perr := ParseBatchFileName(e.Name())
		if perr != nil {
			continue
		}
		if bf.Seq > max {
			max = bf.Seq
		}
	}
	return max + 1, nil
}
