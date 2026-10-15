package workspace

import (
	"errors"
	"golang.org/x/sys/windows"
	"os"
	"path/filepath"
	"runtime"

	"unsafe"
)

// Every component is opened relative to a held directory without following reparse points.
func nativeOpen(parent *os.File, name string, dir, create, existingDir bool) (*os.File, error) {
	return nativeAccess(parent, name, dir, create, existingDir, !dir)
}
func nativeAccess(parent *os.File, name string, dir, create, existingDir, mutation bool) (*os.File, error) {
	u, e := windows.NewNTUnicodeString(name)
	if e != nil {
		return nil, e
	}
	oa := windows.OBJECT_ATTRIBUTES{RootDirectory: windows.Handle(parent.Fd()), ObjectName: u, Attributes: windows.OBJ_CASE_INSENSITIVE}
	oa.Length = uint32(unsafe.Sizeof(oa))
	var h windows.Handle
	var ios windows.IO_STATUS_BLOCK
	disp := uint32(windows.FILE_OPEN)
	if create {
		disp = windows.FILE_CREATE
		if existingDir {
			disp = windows.FILE_OPEN_IF
		}
	}
	opts := uint32(windows.FILE_OPEN_REPARSE_POINT | windows.FILE_SYNCHRONOUS_IO_NONALERT | windows.FILE_OPEN_FOR_BACKUP_INTENT)
	access := uint32(windows.FILE_GENERIC_READ)
	if mutation {
		access |= windows.DELETE
	}
	share := uint32(windows.FILE_SHARE_READ)
	if dir {
		opts |= windows.FILE_DIRECTORY_FILE
		share |= windows.FILE_SHARE_WRITE
		access = windows.FILE_GENERIC_READ
	}
	if create && !dir {
		access |= windows.FILE_GENERIC_WRITE
	}
	e = windows.NtCreateFile(&h, access, &oa, &ios, nil, 0, share, disp, opts, 0, 0)
	runtime.KeepAlive(parent)
	if e != nil {
		var status windows.NTStatus
		if errors.As(e, &status) {
			e = status.Errno()
		}
		return nil, &os.PathError{Op: "open", Path: name, Err: e}
	}
	f := os.NewFile(uintptr(h), name)
	if e = checkHandle(f, false); e != nil {
		f.Close()
		return nil, e
	}
	// Reject alternate casing and 8.3 aliases so SQLite path uniqueness matches
	// the observed Windows filename. The final path is read from this handle.
	if name != "." {
		buf := make([]uint16, 32768)
		n, err := windows.GetFinalPathNameByHandle(h, &buf[0], uint32(len(buf)), 0)
		if err != nil || n >= uint32(len(buf)) {
			f.Close()
			if err != nil {
				return nil, err
			}
			return nil, invalid("Final path exceeds limit")
		}
		if filepath.Base(windows.UTF16ToString(buf[:n])) != name {
			f.Close()
			return nil, invalid("Use the canonical filename casing")
		}
	}
	return f, nil
}

type renameInformation struct {
	ReplaceIfExists uint32
	RootDirectory   windows.Handle
	FileNameLength  uint32
	FileName        [1]uint16
}

func nativeRename(source, parent *os.File, name string) error {
	if e := validLeaf(name); e != nil {
		return e
	}
	if e := checkHandle(source, false); e != nil {
		return e
	}
	if e := checkHandle(parent, false); e != nil {
		return e
	}
	u, e := windows.UTF16FromString(name)
	if e != nil {
		return e
	}
	n := (len(u) - 1) * 2
	var layout renameInformation
	b := make([]byte, int(unsafe.Offsetof(layout.FileName))+n)
	info := (*renameInformation)(unsafe.Pointer(&b[0]))
	info.RootDirectory = windows.Handle(parent.Fd())
	info.FileNameLength = uint32(n)
	copy(unsafe.Slice(&info.FileName[0], len(u)-1), u)
	var ios windows.IO_STATUS_BLOCK
	e = windows.NtSetInformationFile(windows.Handle(source.Fd()), &ios, &b[0], uint32(len(b)), windows.FileRenameInformation)
	runtime.KeepAlive(source)
	runtime.KeepAlive(parent)
	if e != nil {
		var status windows.NTStatus
		if errors.As(e, &status) {
			e = status.Errno()
		}
		return &os.PathError{Op: "rename", Path: name, Err: e}
	}
	return nil
}

func nativeRemove(f *os.File) error {
	var yes byte = 1
	e := windows.SetFileInformationByHandle(windows.Handle(f.Fd()), windows.FileDispositionInfo, &yes, 1)
	runtime.KeepAlive(f)
	return e
}
