package state

import (
	"path/filepath"
	"testing"

	"github.com/jhumanj/tailpreview/internal/model"
)

func TestStoreRoundTripAndPermissions(t *testing.T) {
	dir := t.TempDir()
	store := Store{RegistryPath: filepath.Join(dir, "registry.json"), LockPath: filepath.Join(dir, "registry.lock")}
	locked, err := store.Lock()
	if err != nil {
		t.Fatal(err)
	}
	registry := model.NewRegistry()
	registry.Previews = append(registry.Previews, model.Preview{ID: "one", Name: "one"})
	if err := locked.Save(registry); err != nil {
		t.Fatal(err)
	}
	loaded, err := locked.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Previews) != 1 || loaded.Previews[0].ID != "one" {
		t.Fatalf("unexpected registry: %#v", loaded)
	}
	if err := locked.Close(); err != nil {
		t.Fatal(err)
	}
	info, err := filepath.Glob(filepath.Join(dir, ".registry-*.tmp"))
	if err != nil || len(info) != 0 {
		t.Fatalf("temporary registry files remain: %v", info)
	}
}
