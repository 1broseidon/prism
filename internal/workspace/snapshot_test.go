package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCreateSnapshotFiltersAndDiffs(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "README.md", "hello\n")
	writeFile(t, root, ".env", "SECRET=value\n")
	writeFile(t, root, "src/app.txt", "v1\n")

	snap, err := CreateSnapshot(root, SnapshotPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if snap.BaseID == "" || len(snap.Archive) == 0 {
		t.Fatalf("snapshot missing base/archive: %+v", snap)
	}
	for _, f := range snap.Files {
		if f.Path == ".env" {
			t.Fatal(".env should be excluded by default")
		}
	}

	writeFile(t, root, "src/app.txt", "v2\n")
	writeFile(t, root, "src/new.txt", "new\n")
	if removeErr := os.Remove(filepath.Join(root, "README.md")); removeErr != nil {
		t.Fatal(removeErr)
	}
	next, err := CreateSnapshot(root, SnapshotPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	changes, err := ChangeSetFromArchives(snap.BaseID, snap.Archive, next.Archive)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes.Files) != 3 {
		t.Fatalf("changes = %d, want 3: %+v", len(changes.Files), changes.Files)
	}
	kinds := map[string]string{}
	for _, ch := range changes.Files {
		kinds[ch.Path] = ch.Type
	}
	if kinds["README.md"] != "delete" || kinds["src/app.txt"] != "modify" || kinds["src/new.txt"] != "add" {
		t.Fatalf("unexpected change kinds: %+v", kinds)
	}
}

func TestApplyChangeSetDetectsConflict(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "task.txt", "base\n")
	base, err := CreateSnapshot(root, SnapshotPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, root, "task.txt", "from sandbox\n")
	next, err := CreateSnapshot(root, SnapshotPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	changes, err := ChangeSetFromArchives(base.BaseID, base.Archive, next.Archive)
	if err != nil {
		t.Fatal(err)
	}

	local := t.TempDir()
	writeFile(t, local, "task.txt", "local edit\n")
	result, err := ApplyChangeSet(local, changes, SnapshotPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Applied != 0 || len(result.Conflicts) != 1 {
		t.Fatalf("apply result = %+v, want one conflict", result)
	}

	clean := t.TempDir()
	writeFile(t, clean, "task.txt", "base\n")
	result, err = ApplyChangeSet(clean, changes, SnapshotPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Applied != 1 || len(result.Conflicts) != 0 {
		t.Fatalf("apply result = %+v, want applied", result)
	}
	data, err := os.ReadFile(filepath.Join(clean, "task.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "from sandbox\n" {
		t.Fatalf("file = %q", data)
	}
}

func writeFile(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
