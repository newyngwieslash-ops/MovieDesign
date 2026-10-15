package workspace

import (
	"golang.org/x/sys/windows"
	"os"
)

func fileIdentity(f *os.File) (int64, int64, error) {
	var i windows.ByHandleFileInformation
	e := windows.GetFileInformationByHandle(windows.Handle(f.Fd()), &i)
	return int64(i.VolumeSerialNumber), int64(uint64(i.FileIndexHigh)<<32 | uint64(i.FileIndexLow)), e
}
func checkHandle(f *os.File, regular bool) error {
	h := windows.Handle(f.Fd())
	kind, e := windows.GetFileType(h)
	if e != nil {
		return e
	}
	if kind != windows.FILE_TYPE_DISK {
		return invalid("Only disk files are supported")
	}
	var i windows.ByHandleFileInformation
	if e = windows.GetFileInformationByHandle(h, &i); e != nil {
		return e
	}
	if i.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return invalid("Reparse points are forbidden")
	}
	if regular && (i.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0 || i.NumberOfLinks != 1) {
		return invalid("Expected a regular file with one link")
	}
	if i.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 && i.NumberOfLinks != 1 {
		return invalid("Hard links are forbidden")
	}
	return nil
}
