package skill

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Rush1994/SkillPort/internal/errs"
)

// WorkingFiles includes excluded and invalid-metadata files when comparing local
// changes. It hashes bytes without printing content or requiring validation to pass.
func WorkingFiles(root string) ([]File, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, errs.New(6, "Invalid local directory.")
	}
	if err = CheckParents(root); err != nil {
		return nil, err
	}
	files := []File{}
	count := 0
	err = filepath.WalkDir(root, func(full string, d fs.DirEntry, e error) error {
		if e != nil {
			return errs.New(6, "Cannot inspect working files.")
		}
		if full == root {
			return nil
		}
		rel, _ := filepath.Rel(root, full)
		p := filepath.ToSlash(rel)
		if IsStateDirectory(strings.ToLower(strings.Split(p, "/")[0])) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		count++
		if count > MaxEntries {
			return errs.New(6, "Working directory entry limit exceeded.")
		}
		if !SafePath(p) {
			return errs.New(6, "Invalid working file path.")
		}
		info, e := os.Lstat(full)
		if e != nil {
			return errs.New(6, "Cannot inspect working file.")
		}
		if e = CheckFile(full, info); e != nil {
			return e
		}
		if d.IsDir() {
			return nil
		}
		if !info.Mode().IsRegular() {
			return errs.New(6, "Unsupported working file type.")
		}
		f, e := os.Open(full)
		if e != nil {
			return errs.New(6, "Cannot read working file.")
		}
		h := sha256.New()
		n, e := io.Copy(h, io.LimitReader(f, MaxTotal+1))
		f.Close()
		if e != nil || n > MaxTotal {
			return errs.New(6, "Cannot hash working file within size limit.")
		}
		files = append(files, File{Path: p, Hash: hex.EncodeToString(h.Sum(nil)), Size: n})
		return nil
	})
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, err
}
