// Package workspace implements snapshot, diff, and apply primitives for
// sandboxed workspace copies.
package workspace

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	// DefaultMaxSnapshotBytes is the default upper bound for a workspace snapshot.
	DefaultMaxSnapshotBytes int64 = 32 << 20

	// WriteModeSandboxOnly keeps sandbox changes away from the local workspace.
	WriteModeSandboxOnly = "sandbox_only"
	// WriteModeStage stages sandbox changes for explicit operator approval.
	WriteModeStage = "stage"
	// WriteModeAutoApply applies allowed sandbox changes after tool calls.
	WriteModeAutoApply = "auto_apply"
)

// DefaultExclude contains sensitive or high-churn paths omitted from snapshots.
var DefaultExclude = []string{
	".git/**",
	".env",
	".env.*",
	".ssh/**",
	".aws/**",
	".azure/**",
	".gcloud/**",
	"node_modules/**",
	"vendor/**",
	"dist/**",
	"build/**",
	"coverage/**",
	"*.pem",
	"*.key",
	"*.p12",
	"*.pfx",
}

// SnapshotPolicy controls which files are included in a workspace snapshot.
type SnapshotPolicy struct {
	Include  []string `json:"include,omitempty"`
	Exclude  []string `json:"exclude,omitempty"`
	MaxBytes int64    `json:"max_bytes,omitempty"`
}

// Snapshot is a compressed archive plus manifest for a workspace revision.
type Snapshot struct {
	BaseID      string     `json:"base_id"`
	CreatedAt   time.Time  `json:"created_at"`
	Archive     []byte     `json:"archive"`
	Files       []FileInfo `json:"files"`
	Directories []string   `json:"directories,omitempty"`
	TotalBytes  int64      `json:"total_bytes"`
}

// FileInfo describes one file inside a workspace snapshot.
type FileInfo struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// ChangeSet describes the difference between a base snapshot and sandbox state.
type ChangeSet struct {
	BaseID      string    `json:"base_id"`
	GeneratedAt time.Time `json:"generated_at"`
	Files       []Change  `json:"files"`
}

// Change is one added, modified, or deleted file in a ChangeSet.
type Change struct {
	Path       string `json:"path"`
	Type       string `json:"type"`
	OldSHA256  string `json:"old_sha256,omitempty"`
	NewSHA256  string `json:"new_sha256,omitempty"`
	NewContent []byte `json:"new_content,omitempty"`
	Binary     bool   `json:"binary,omitempty"`
	Preview    string `json:"preview,omitempty"`
}

// ApplyResult reports how many changes applied and which paths conflicted.
type ApplyResult struct {
	Applied   int      `json:"applied"`
	Conflicts []string `json:"conflicts,omitempty"`
}

type archiveFile struct {
	info    FileInfo
	content []byte
}

type archiveEntries struct {
	files       map[string]archiveFile
	directories []string
}

// NormalizePolicy fills snapshot policy defaults.
func NormalizePolicy(policy SnapshotPolicy) SnapshotPolicy {
	out := policy
	out.Include = cleanPatterns(out.Include)
	out.Exclude = append(cleanPatterns(DefaultExclude), cleanPatterns(out.Exclude)...)
	if out.MaxBytes <= 0 {
		out.MaxBytes = DefaultMaxSnapshotBytes
	}
	return out
}

// CreateSnapshot builds a compressed tar snapshot from root under policy.
func CreateSnapshot(root string, policy SnapshotPolicy) (*Snapshot, error) { //nolint:gocyclo // filesystem policy checks are intentionally centralized
	root, err := filepath.Abs(strings.TrimSpace(root))
	if err != nil {
		return nil, err
	}
	if root == "" {
		return nil, errors.New("workspace root is required")
	}
	policy = NormalizePolicy(policy)

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	files := make([]FileInfo, 0)
	directories := make([]string, 0)
	var total int64

	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if p == root {
			return nil
		}
		rel, relErr := filepath.Rel(root, p)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)
		if !safeRelPath(rel) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if !allowedByPolicy(rel, true, policy) {
				return filepath.SkipDir
			}
			info, infoErr := d.Info()
			if infoErr != nil {
				return infoErr
			}
			directories = append(directories, rel)
			return tw.WriteHeader(&tar.Header{
				Name:     rel,
				Typeflag: tar.TypeDir,
				Mode:     0o755,
				ModTime:  info.ModTime(),
			})
		}
		if !allowedByPolicy(rel, false, policy) {
			return nil
		}
		info, infoErr := d.Info()
		if infoErr != nil {
			return infoErr
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return nil
		}
		if total+info.Size() > policy.MaxBytes {
			return fmt.Errorf("workspace snapshot exceeds max_bytes (%d)", policy.MaxBytes)
		}
		data, readErr := os.ReadFile(p) //nolint:gosec // p is produced by walking the configured workspace root
		if readErr != nil {
			return readErr
		}
		total += int64(len(data))
		sum := sha256.Sum256(data)
		fileInfo := FileInfo{Path: rel, Size: int64(len(data)), SHA256: hex.EncodeToString(sum[:])}
		files = append(files, fileInfo)

		hdr := &tar.Header{
			Name:    rel,
			Mode:    0o644,
			Size:    int64(len(data)),
			ModTime: info.ModTime(),
		}
		if headerErr := tw.WriteHeader(hdr); headerErr != nil {
			return headerErr
		}
		if _, writeErr := tw.Write(data); writeErr != nil {
			return writeErr
		}
		return nil
	})
	if err != nil {
		_ = tw.Close()
		_ = gz.Close()
		return nil, err
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	sort.Strings(directories)
	return &Snapshot{
		BaseID:      snapshotID(files, directories),
		CreatedAt:   time.Now().UTC(),
		Archive:     buf.Bytes(),
		Files:       files,
		Directories: directories,
		TotalBytes:  total,
	}, nil
}

// ChangeSetFromArchives compares a base snapshot archive to current sandbox state.
func ChangeSetFromArchives(baseID string, baseArchive, currentArchive []byte) (*ChangeSet, error) {
	baseFiles, err := readArchive(baseArchive)
	if err != nil {
		return nil, fmt.Errorf("read base archive: %w", err)
	}
	currentFiles, err := readArchive(currentArchive)
	if err != nil {
		return nil, fmt.Errorf("read current archive: %w", err)
	}

	paths := make(map[string]struct{}, len(baseFiles)+len(currentFiles))
	for p := range baseFiles {
		paths[p] = struct{}{}
	}
	for p := range currentFiles {
		paths[p] = struct{}{}
	}
	ordered := make([]string, 0, len(paths))
	for p := range paths {
		ordered = append(ordered, p)
	}
	sort.Strings(ordered)

	changes := make([]Change, 0)
	for _, p := range ordered {
		oldFile, hadOld := baseFiles[p]
		newFile, hasNew := currentFiles[p]
		switch {
		case !hadOld && hasNew:
			changes = append(changes, buildChange(p, "add", "", newFile.info.SHA256, newFile.content))
		case hadOld && !hasNew:
			changes = append(changes, Change{Path: p, Type: "delete", OldSHA256: oldFile.info.SHA256})
		case hadOld && hasNew && oldFile.info.SHA256 != newFile.info.SHA256:
			changes = append(changes, buildChange(p, "modify", oldFile.info.SHA256, newFile.info.SHA256, newFile.content))
		}
	}

	return &ChangeSet{BaseID: baseID, GeneratedAt: time.Now().UTC(), Files: changes}, nil
}

// SnapshotFromArchive reconstructs snapshot metadata from an archive.
func SnapshotFromArchive(archive []byte) (*Snapshot, error) {
	entries, err := readArchiveEntries(archive)
	if err != nil {
		return nil, err
	}
	files := make([]FileInfo, 0, len(entries.files))
	var total int64
	for _, f := range entries.files {
		files = append(files, f.info)
		total += f.info.Size
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	directories := append([]string(nil), entries.directories...)
	sort.Strings(directories)
	return &Snapshot{
		BaseID:      snapshotID(files, directories),
		CreatedAt:   time.Now().UTC(),
		Archive:     archive,
		Files:       files,
		Directories: directories,
		TotalBytes:  total,
	}, nil
}

// ApplyChangeSet applies a staged ChangeSet to root with hash conflict checks.
func ApplyChangeSet(root string, changes *ChangeSet, policy SnapshotPolicy) (*ApplyResult, error) { //nolint:gocyclo // apply is the policy and conflict boundary
	if changes == nil {
		return &ApplyResult{}, nil
	}
	root, err := filepath.Abs(strings.TrimSpace(root))
	if err != nil {
		return nil, err
	}
	policy = NormalizePolicy(policy)
	result := &ApplyResult{}
	for _, ch := range changes.Files {
		if !allowedByPolicy(ch.Path, false, policy) {
			result.Conflicts = append(result.Conflicts, ch.Path+": path is not allowed by workspace policy")
			continue
		}
		target, err := safeJoin(root, ch.Path)
		if err != nil {
			result.Conflicts = append(result.Conflicts, ch.Path+": "+err.Error())
			continue
		}
		currentHash, exists, err := fileHash(target)
		if err != nil {
			result.Conflicts = append(result.Conflicts, ch.Path+": "+err.Error())
			continue
		}
		if ch.OldSHA256 != "" && currentHash != ch.OldSHA256 {
			result.Conflicts = append(result.Conflicts, ch.Path+": local file changed since snapshot")
			continue
		}
		if ch.Type == "add" && exists {
			result.Conflicts = append(result.Conflicts, ch.Path+": local file already exists")
			continue
		}
		switch ch.Type {
		case "add", "modify":
			if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
				return result, err
			}
			if err := os.WriteFile(target, ch.NewContent, 0o644); err != nil { //nolint:gosec // target is confined by safeJoin
				return result, err
			}
		case "delete":
			if exists {
				if err := os.Remove(target); err != nil {
					return result, err
				}
			}
		default:
			result.Conflicts = append(result.Conflicts, ch.Path+": unknown change type "+ch.Type)
			continue
		}
		result.Applied++
	}
	return result, nil
}

// ArchiveFromReader converts an uncompressed tar stream into snapshot archive format.
func ArchiveFromReader(r io.Reader) ([]byte, error) {
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		return nil, err
	}
	return gzipArchive(buf.Bytes())
}

func gzipArchive(tarData []byte) ([]byte, error) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if _, err := gz.Write(tarData); err != nil {
		_ = gz.Close()
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// TarForContainer rewrites a snapshot archive as an uncompressed Docker copy tar.
func TarForContainer(snapshotArchive []byte, uid, gid int) (io.Reader, error) {
	entries, err := readArchiveEntries(snapshotArchive)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	if err := tw.WriteHeader(&tar.Header{
		Name:     ".",
		Typeflag: tar.TypeDir,
		Mode:     0o775,
		Uid:      uid,
		Gid:      gid,
	}); err != nil {
		return nil, err
	}
	directories := directoriesForTar(entries)
	for _, dir := range directories {
		if err := tw.WriteHeader(&tar.Header{
			Name:     dir,
			Typeflag: tar.TypeDir,
			Mode:     0o755,
			Uid:      uid,
			Gid:      gid,
		}); err != nil {
			return nil, err
		}
	}
	paths := make([]string, 0, len(entries.files))
	for p := range entries.files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		f := entries.files[p]
		hdr := &tar.Header{
			Name: p,
			Mode: 0o644,
			Size: int64(len(f.content)),
			Uid:  uid,
			Gid:  gid,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return nil, err
		}
		if _, err := tw.Write(f.content); err != nil {
			return nil, err
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	return bytes.NewReader(buf.Bytes()), nil
}

func readArchive(data []byte) (map[string]archiveFile, error) {
	entries, err := readArchiveEntries(data)
	if err != nil {
		return nil, err
	}
	return entries.files, nil
}

func readArchiveEntries(data []byte) (archiveEntries, error) {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return archiveEntries{}, err
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	files := make(map[string]archiveFile)
	directories := make(map[string]struct{})
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return archiveEntries{}, err
		}
		if hdr == nil {
			continue
		}
		name := path.Clean(filepath.ToSlash(hdr.Name))
		if hdr.FileInfo().IsDir() || hdr.Typeflag == tar.TypeDir {
			if name == "." {
				continue
			}
			if !safeRelPath(name) {
				return archiveEntries{}, fmt.Errorf("unsafe archive path %q", hdr.Name)
			}
			directories[name] = struct{}{}
			continue
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		if !safeRelPath(name) {
			return archiveEntries{}, fmt.Errorf("unsafe archive path %q", hdr.Name)
		}
		content, err := io.ReadAll(tr)
		if err != nil {
			return archiveEntries{}, err
		}
		sum := sha256.Sum256(content)
		files[name] = archiveFile{
			info: FileInfo{
				Path:   name,
				Size:   int64(len(content)),
				SHA256: hex.EncodeToString(sum[:]),
			},
			content: content,
		}
	}
	dirs := make([]string, 0, len(directories))
	for dir := range directories {
		dirs = append(dirs, dir)
	}
	sort.Strings(dirs)
	return archiveEntries{files: files, directories: dirs}, nil
}

func buildChange(p, typ, oldHash, newHash string, content []byte) Change {
	binary := !isText(content)
	preview := ""
	if !binary {
		preview = string(content)
		if len(preview) > 4096 {
			preview = preview[:4096] + "\n…"
		}
	}
	return Change{
		Path:       p,
		Type:       typ,
		OldSHA256:  oldHash,
		NewSHA256:  newHash,
		NewContent: content,
		Binary:     binary,
		Preview:    preview,
	}
}

func fileHash(target string) (hash string, exists bool, err error) {
	info, err := os.Lstat(target)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", false, nil
		}
		return "", false, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", true, errors.New("refusing to apply through symlink")
	}
	if !info.Mode().IsRegular() {
		return "", true, errors.New("target is not a regular file")
	}
	data, err := os.ReadFile(target) //nolint:gosec // target is confined by safeJoin
	if err != nil {
		return "", true, err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), true, nil
}

func snapshotID(files []FileInfo, directories []string) string {
	h := sha256.New()
	for _, dir := range directories {
		_, _ = h.Write([]byte("dir"))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(dir))
		_, _ = h.Write([]byte{0})
	}
	for _, f := range files {
		_, _ = h.Write([]byte("file"))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(f.Path))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(f.SHA256))
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

func directoriesForTar(entries archiveEntries) []string {
	seen := make(map[string]struct{}, len(entries.directories)+len(entries.files))
	for _, dir := range entries.directories {
		if safeRelPath(dir) {
			seen[dir] = struct{}{}
		}
	}
	for filePath := range entries.files {
		dir := path.Dir(filePath)
		for dir != "." && safeRelPath(dir) {
			seen[dir] = struct{}{}
			dir = path.Dir(dir)
		}
	}
	dirs := make([]string, 0, len(seen))
	for dir := range seen {
		dirs = append(dirs, dir)
	}
	sort.Strings(dirs)
	return dirs
}

func cleanPatterns(patterns []string) []string {
	out := make([]string, 0, len(patterns))
	seen := make(map[string]struct{}, len(patterns))
	for _, p := range patterns {
		p = path.Clean(filepath.ToSlash(strings.TrimSpace(p)))
		if p == "." || p == "" {
			continue
		}
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	return out
}

func allowedByPolicy(rel string, isDir bool, policy SnapshotPolicy) bool {
	rel = path.Clean(filepath.ToSlash(rel))
	if !safeRelPath(rel) {
		return false
	}
	for _, p := range policy.Exclude {
		if matchesPattern(p, rel) || (isDir && strings.HasPrefix(p, rel+"/")) {
			return false
		}
	}
	if len(policy.Include) == 0 {
		return true
	}
	for _, p := range policy.Include {
		if matchesPattern(p, rel) || (isDir && strings.HasPrefix(p, rel+"/")) {
			return true
		}
	}
	return false
}

func matchesPattern(pattern, rel string) bool {
	pattern = filepath.ToSlash(pattern)
	rel = filepath.ToSlash(rel)
	if strings.HasSuffix(pattern, "/**") {
		prefix := strings.TrimSuffix(pattern, "/**")
		return rel == prefix || strings.HasPrefix(rel, prefix+"/")
	}
	if ok, _ := path.Match(pattern, rel); ok {
		return true
	}
	if !strings.Contains(pattern, "/") {
		ok, _ := path.Match(pattern, path.Base(rel))
		return ok
	}
	return false
}

func safeRelPath(rel string) bool {
	if rel == "" || rel == "." || strings.HasPrefix(rel, "/") {
		return false
	}
	clean := path.Clean(filepath.ToSlash(rel))
	return clean == rel && clean != ".." && !strings.HasPrefix(clean, "../")
}

func safeJoin(root, rel string) (string, error) {
	if !safeRelPath(rel) {
		return "", errors.New("unsafe relative path")
	}
	target := filepath.Join(root, filepath.FromSlash(rel))
	cleanRoot := filepath.Clean(root)
	cleanTarget := filepath.Clean(target)
	if cleanTarget != cleanRoot && !strings.HasPrefix(cleanTarget, cleanRoot+string(os.PathSeparator)) {
		return "", errors.New("path escapes workspace root")
	}
	return cleanTarget, nil
}

func isText(data []byte) bool {
	if len(data) == 0 {
		return true
	}
	if bytes.IndexByte(data, 0) >= 0 {
		return false
	}
	sample := data
	if len(sample) > 4096 {
		sample = sample[:4096]
	}
	return utf8.Valid(sample)
}
