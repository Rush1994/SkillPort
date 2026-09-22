package skill

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Rush1994/SkillPort/internal/errs"
)

func Pack(s Snapshot) ([]byte, error) {
	var b bytes.Buffer
	z, _ := gzip.NewWriterLevel(&b, gzip.BestCompression)
	z.Header.OS = 255
	t := tar.NewWriter(z)
	for _, f := range s.Files {
		if err := t.WriteHeader(&tar.Header{Name: f.Path, Size: int64(len(f.Data)), Mode: f.Mode, Typeflag: tar.TypeReg, Format: tar.FormatPAX}); err != nil {
			return nil, err
		}
		if _, err := t.Write(f.Data); err != nil {
			return nil, err
		}
	}
	if err := t.Close(); err != nil {
		return nil, err
	}
	if err := z.Close(); err != nil {
		return nil, err
	}
	if b.Len() > MaxTotal {
		return nil, errs.New(6, "LIMIT_ARCHIVE exceeded.")
	}
	return b.Bytes(), nil
}

// Extract is only called with a newly created private staging directory.
func Extract(data []byte, root string) error {
	if len(data) > MaxTotal {
		return errs.New(6, "LIMIT_ARCHIVE exceeded.")
	}
	z, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return errs.New(6, "Invalid gzip archive.")
	}
	defer z.Close()
	t := tar.NewReader(io.LimitReader(z, 200<<20+1))
	seen := map[string]bool{}
	components := map[string]string{}
	var total int64
	count := 0
	for {
		h, e := t.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			return errs.New(6, "Invalid tar archive.")
		}
		count++
		if count > MaxEntries {
			return errs.New(6, "LIMIT_ENTRIES exceeded.")
		}
		p := h.Name
		if !SafePath(p) || (h.Typeflag != tar.TypeReg && h.Typeflag != tar.TypeRegA) {
			return errs.New(6, "Unsafe archive entry or unsupported file type.")
		}
		if Excluded(p) != "" {
			return errs.New(6, "Remote archive contains excluded/sensitive paths.")
		}
		key := strings.ToLower(p)
		if seen[key] {
			return errs.New(6, "Duplicate/case-colliding archive path.")
		}
		seen[key] = true
		parts := strings.Split(p, "/")
		for i := range parts {
			component := strings.Join(parts[:i+1], "/")
			key := strings.ToLower(component)
			if prior, ok := components[key]; ok && prior != component {
				return errs.New(6, "Case-colliding archive parent path.")
			}
			components[key] = component
			if i < len(parts)-1 && seen[key] {
				return errs.New(6, "File used as archive directory.")
			}
		}
		if h.Size < 0 || h.Size > MaxFile {
			return errs.New(6, "LIMIT_FILE exceeded.")
		}
		total += h.Size
		if total > MaxTotal {
			return errs.New(6, "LIMIT_TOTAL exceeded.")
		}
		full := filepath.Join(root, filepath.FromSlash(p))
		if err = os.MkdirAll(filepath.Dir(full), 0700); err != nil {
			return errs.New(6, "Cannot create staged directory.")
		}
		f, e := os.OpenFile(full, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			return errs.New(6, "Conflicting archive path.")
		}
		n, e := io.Copy(f, io.LimitReader(t, MaxFile+1))
		ce := f.Close()
		if e != nil || ce != nil || n != h.Size {
			return errs.New(6, "Truncated or oversized archive entry.")
		}
		mode := os.FileMode(0644)
		if h.Mode&0111 != 0 {
			mode = 0755
		}
		if err = os.Chmod(full, mode); err != nil {
			return errs.New(6, "Cannot set staged file mode.")
		}
	}
	// Read through gzip trailer to enforce checksum; reject nonzero trailing payload.
	tail, e := io.ReadAll(io.LimitReader(z, 1<<20))
	if e != nil || len(bytes.Trim(tail, "\x00")) != 0 || len(tail) >= 1<<20 {
		return errs.New(6, "Invalid archive trailer.")
	}
	return nil
}

// ArchiveModes reads permission metadata only after Extract has validated the layer.
func ArchiveModes(data []byte) map[string]int64 {
	modes := map[string]int64{}
	z, e := gzip.NewReader(bytes.NewReader(data))
	if e != nil {
		return modes
	}
	defer z.Close()
	t := tar.NewReader(z)
	for {
		h, e := t.Next()
		if e != nil {
			break
		}
		mode := int64(0644)
		if h.Mode&0111 != 0 {
			mode = 0755
		}
		modes[h.Name] = mode
	}
	return modes
}
