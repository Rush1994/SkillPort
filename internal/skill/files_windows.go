//go:build windows

package skill

import (
	"github.com/Rush1994/SkillPort/internal/errs"
	"os"
	"syscall"
)

func CheckFile(p string, info os.FileInfo) error {
	ptr, err := syscall.UTF16PtrFromString(p)
	if err != nil {
		return errs.New(6, "Invalid Windows path.")
	}
	attrs, err := syscall.GetFileAttributes(ptr)
	if err != nil || attrs&syscall.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return errs.New(6, "Links/reparse points or inaccessible paths are not supported.")
	}
	if info.Mode().IsRegular() {
		h, err := syscall.CreateFile(ptr, 0, syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|syscall.FILE_SHARE_DELETE, nil, syscall.OPEN_EXISTING, syscall.FILE_ATTRIBUTE_NORMAL, 0)
		if err != nil {
			return errs.New(6, "Cannot inspect Windows file.")
		}
		defer syscall.CloseHandle(h)
		var stat syscall.ByHandleFileInformation
		if syscall.GetFileInformationByHandle(h, &stat) != nil || stat.NumberOfLinks > 1 {
			return errs.New(6, "Hardlinks or unreadable file metadata are not supported.")
		}
	}
	return nil
}
