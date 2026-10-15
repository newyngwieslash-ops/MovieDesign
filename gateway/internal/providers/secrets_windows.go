package providers

import (
	"errors"
	"golang.org/x/sys/windows"
	"os"
	"path/filepath"
	"runtime"
	"unsafe"
)

type SecretStore interface {
	Put(id, value string) error
	Get(id string) (string, error)
	Delete(id string) error
}
type DPAPIStore struct {
	dir           string
	beforePublish func(string) // test-only timing seam; nil in production
}

func OpenSecrets(dir string) (*DPAPIStore, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	return &DPAPIStore{dir: dir}, nil
}
func (s *DPAPIStore) path(id string) (string, error) {
	if !identifier.MatchString(id) || len(id) > 128 {
		return "", errors.New("invalid credential version")
	}
	return filepath.Join(s.dir, id+".dpapi"), nil
}
func crypt(data []byte, encrypt bool) ([]byte, error) {
	if len(data) == 0 {
		return nil, errors.New("empty secret")
	}
	in := windows.DataBlob{Size: uint32(len(data)), Data: &data[0]}
	var out windows.DataBlob
	var err error
	if encrypt {
		err = windows.CryptProtectData(&in, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out)
	} else {
		err = windows.CryptUnprotectData(&in, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out)
	}
	runtime.KeepAlive(data)
	if err != nil {
		return nil, errors.New("credential encryption unavailable")
	}
	defer windows.LocalFree(windows.Handle(uintptr(unsafe.Pointer(out.Data))))
	result := append([]byte(nil), unsafe.Slice(out.Data, int(out.Size))...)
	clear(unsafe.Slice(out.Data, int(out.Size)))
	return result, nil
}

// Immutable version files are flushed and renamed before SQL publishes their identity.
// A crash before SQL commit can leave only an unreferenced encrypted file; never delete
// old versions during rotation/removal because a frozen task may still reference them.
func (s *DPAPIStore) Put(id, value string) error {
	path, err := s.path(id)
	if err != nil {
		return err
	}
	if _, err = os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		return errors.New("credential version already exists or is inaccessible")
	}
	data, err := crypt([]byte(value), true)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(s.dir, ".pending-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	from, err := windows.UTF16PtrFromString(f.Name())
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	if s.beforePublish != nil {
		s.beforePublish(path)
	}
	return windows.MoveFileEx(from, to, windows.MOVEFILE_WRITE_THROUGH)
}
func (s *DPAPIStore) Get(id string) (string, error) {
	path, err := s.path(id)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", errors.New("credential version unavailable")
	}
	plain, err := crypt(data, false)
	if err != nil {
		return "", err
	}
	defer clear(plain)
	return string(plain), nil
}

// Delete is only for explicitly proven unreferenced versions, never configuration deletion.
func (s *DPAPIStore) Delete(id string) error {
	path, err := s.path(id)
	if err != nil {
		return err
	}
	return os.Remove(path)
}
