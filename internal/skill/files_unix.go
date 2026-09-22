//go:build !windows

package skill

import (
	"github.com/Rush1994/SkillPort/internal/errs"
	"os"
	"syscall"
)

func CheckFile(p string, info os.FileInfo) error {
	if info.Mode()&os.ModeSymlink != 0 {
		return errs.New(6, "Symbolic links are not supported.")
	}
	if s, ok := info.Sys().(*syscall.Stat_t); ok && info.Mode().IsRegular() && s.Nlink > 1 {
		return errs.New(6, "Hardlinks are not supported.")
	}
	return nil
}
