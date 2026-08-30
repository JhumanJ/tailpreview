package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"

	"github.com/jhumanj/tailpreview/internal/model"
)

type Store struct {
	RegistryPath string
	LockPath     string
}

type Locked struct {
	store Store
	file  *os.File
}

func (s Store) Lock() (*Locked, error) {
	if err := os.MkdirAll(filepath.Dir(s.LockPath), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(s.LockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		_ = f.Close()
		return nil, err
	}
	return &Locked{store: s, file: f}, nil
}

func (l *Locked) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	unlockErr := syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	closeErr := l.file.Close()
	l.file = nil
	if unlockErr != nil {
		return unlockErr
	}
	return closeErr
}

func (l *Locked) Load() (model.Registry, error) {
	f, err := os.Open(l.store.RegistryPath)
	if errors.Is(err, os.ErrNotExist) {
		return model.NewRegistry(), nil
	}
	if err != nil {
		return model.Registry{}, err
	}
	defer f.Close()
	decoder := json.NewDecoder(f)
	decoder.DisallowUnknownFields()
	var registry model.Registry
	if err := decoder.Decode(&registry); err != nil {
		return model.Registry{}, fmt.Errorf("decode registry: %w", err)
	}
	if err := ensureEOF(decoder); err != nil {
		return model.Registry{}, err
	}
	if registry.Version != model.RegistryVersion {
		return model.Registry{}, fmt.Errorf("unsupported registry version %d", registry.Version)
	}
	if registry.Reservations == nil {
		registry.Reservations = make(map[string]model.PortReservation)
	}
	if registry.Previews == nil {
		registry.Previews = []model.Preview{}
	}
	return registry, nil
}

func (l *Locked) Save(registry model.Registry) error {
	if err := os.MkdirAll(filepath.Dir(l.store.RegistryPath), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(registry, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(filepath.Dir(l.store.RegistryPath), ".registry-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	defer cleanup()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, l.store.RegistryPath); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(l.store.RegistryPath))
	if err == nil {
		_ = dir.Sync()
		_ = dir.Close()
	}
	return nil
}

func ensureEOF(decoder *json.Decoder) error {
	var extra interface{}
	if err := decoder.Decode(&extra); err == io.EOF {
		return nil
	} else if err != nil {
		return err
	}
	return errors.New("registry contains multiple JSON values")
}
