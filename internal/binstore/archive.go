package binstore

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"path"
	"path/filepath"
	"strings"
)

// ArchiveKind identifies the detected archive format. detectArchiveKind picks
// the right reader based on magic bytes rather than filename — operators
// uploading via raw URL fetch can't be trusted to put .tar.gz in the name.
type ArchiveKind int

const (
	// KindUnknown means the bytes are not a recognized archive. Callers
	// should treat the payload as a single binary candidate.
	KindUnknown ArchiveKind = iota
	// KindZip is a ZIP archive (PK\x03\x04).
	KindZip
	// KindTarGz is a gzip-compressed tarball (\x1f\x8b — gzip magic). The
	// payload is fully decompressed in memory; the per-binary cap caps the
	// decompressed size on output.
	KindTarGz
	// KindTar is an uncompressed tarball ("ustar" marker at offset 257).
	KindTar
)

// ELF header field values used by validateELF. Constants instead of magic
// integers so the error messages and intent are obvious.
const (
	elfMagic       = "\x7fELF"
	elfClass64     = 2    // EI_CLASS: 64-bit
	elfMachineX86  = 0x3E // EM_X86_64
	elfMachineARM  = 0xB7 // EM_AARCH64
	elfMachine386  = 0x03 // EM_386
	elfMachineRISC = 0xF3 // EM_RISCV
	elfMachineMIPS = 0x08 // EM_MIPS
	elfMachinePPC  = 0x15 // EM_PPC64
	elfMachineS390 = 0x16 // EM_S390
	elfHeaderMin   = 20   // bytes we need to sniff: magic+class+ei_data+ei_version+ei_osabi+ei_abiversion+padding+e_type+e_machine
)

// ExtractedBinary is the result of scanning an archive for an ELF candidate.
// Bytes holds the raw decoded binary content; Name is the leaf filename
// suitable for use as a binstore name.
type ExtractedBinary struct {
	Name  string
	Bytes []byte
}

// DetectArchiveKind classifies data by magic bytes only. The check covers the
// three accepted archive formats — zip, tar.gz/.tgz, and uncompressed tar —
// plus everything else as KindUnknown (treated as a raw binary candidate by
// the caller).
func DetectArchiveKind(data []byte) ArchiveKind {
	if len(data) >= 4 && bytes.Equal(data[:4], []byte{'P', 'K', 0x03, 0x04}) {
		return KindZip
	}
	if len(data) >= 2 && data[0] == 0x1f && data[1] == 0x8b {
		return KindTarGz
	}
	// Uncompressed tar has "ustar" at offset 257 of a 512-byte header
	// (POSIX tar) or "ustar  " (GNU). Both forms include the literal
	// "ustar" string at the same offset so we sniff that prefix.
	if len(data) >= 263 {
		marker := data[257:263]
		// Common variants:
		//   ustar\x00 (POSIX)
		//   ustar  \x00 partial — POSIX strict requires NUL at offset 263.
		if bytes.HasPrefix(marker, []byte("ustar")) {
			return KindTar
		}
	}
	return KindUnknown
}

// ELFInfo carries enough of the parsed ELF header to produce a useful error
// when validation fails. Architecture is the human-readable name of the
// detected target so the operator sees "aarch64 binary detected" instead of
// "unsupported e_machine 0xB7".
type ELFInfo struct {
	Class64      bool
	Architecture string
	MachineCode  uint16
	LittleEndian bool
}

// ValidateELF parses the first elfHeaderMin bytes and returns a populated
// ELFInfo. The error is non-nil for anything that isn't a Linux x86_64 ELF.
func ValidateELF(data []byte) (ELFInfo, error) {
	if len(data) < elfHeaderMin {
		return ELFInfo{}, errors.New("not an ELF binary: header too short")
	}
	if string(data[:4]) != elfMagic {
		return ELFInfo{}, errors.New("not an ELF binary: bad magic")
	}
	class := data[4]
	endian := data[5]
	// e_machine lives at offset 18 in the ELF identification block and is
	// a 2-byte little-endian field for little-endian ELFs. We refuse to
	// look at big-endian ELFs at all — Prism only ships an amd64 sandbox.
	if endian != 1 {
		return ELFInfo{LittleEndian: false}, errors.New("not a little-endian ELF (big-endian binaries are not supported)")
	}
	machine := binary.LittleEndian.Uint16(data[18:20])
	info := ELFInfo{
		Class64:      class == elfClass64,
		MachineCode:  machine,
		LittleEndian: true,
		Architecture: archName(machine),
	}
	if class != elfClass64 {
		return info, fmt.Errorf("32-bit ELF binary detected; only linux/amd64 (64-bit) is supported")
	}
	if machine != elfMachineX86 {
		return info, fmt.Errorf("%s binary detected; only linux/amd64 is supported", info.Architecture)
	}
	return info, nil
}

// archName turns an ELF e_machine value into a human-friendly string.
// Unknown codes fall back to a hex literal so the error message still gives
// the operator something searchable.
func archName(machine uint16) string {
	switch machine {
	case elfMachineX86:
		return "x86_64"
	case elfMachineARM:
		return "aarch64"
	case elfMachine386:
		return "i386"
	case elfMachineRISC:
		return "riscv"
	case elfMachineMIPS:
		return "mips"
	case elfMachinePPC:
		return "ppc64"
	case elfMachineS390:
		return "s390"
	default:
		return fmt.Sprintf("unknown-arch-0x%X", machine)
	}
}

// ExtractBinary picks an ELF binary out of the supplied bytes. Behavior:
//   - If data is not an archive (KindUnknown), validate it as an ELF in place
//     and return ExtractedBinary{Name: fallbackName, Bytes: data}.
//   - If data is an archive and contains exactly one ELF, auto-select it.
//   - If the archive contains more than one ELF, require archiveBinaryPath
//     to disambiguate; the path is matched against the archive's entry names.
//   - If the archive contains zero ELFs, return an error.
//
// fallbackName is used as the binary name when the input is a bare ELF
// (otherwise the archive entry's basename is preferred).
//
// helpers obscures the flow more than it helps.
//
//nolint:gocyclo // single decision tree per archive kind; splitting into
func ExtractBinary(data []byte, archiveBinaryPath, fallbackName string) (ExtractedBinary, error) {
	kind := DetectArchiveKind(data)
	if kind == KindUnknown {
		if _, err := ValidateELF(data); err != nil {
			return ExtractedBinary{}, err
		}
		name := strings.TrimSpace(fallbackName)
		if name == "" {
			name = "binary"
		}
		// fallbackName may be a path from a URL — keep only the basename.
		name = path.Base(name)
		if name == "" || name == "." || name == "/" {
			name = "binary"
		}
		return ExtractedBinary{Name: name, Bytes: data}, nil
	}

	candidates, err := scanArchive(data, kind)
	if err != nil {
		return ExtractedBinary{}, err
	}
	elfCandidates := make([]archiveEntry, 0, len(candidates))
	for _, c := range candidates {
		if _, err := ValidateELF(c.body); err != nil {
			continue
		}
		elfCandidates = append(elfCandidates, c)
	}
	if len(elfCandidates) == 0 {
		return ExtractedBinary{}, errors.New("archive contains no linux/amd64 ELF binary")
	}

	archiveBinaryPath = strings.TrimSpace(archiveBinaryPath)
	if archiveBinaryPath != "" {
		cleaned := strings.TrimPrefix(path.Clean(archiveBinaryPath), "./")
		for _, c := range elfCandidates {
			if c.name == cleaned || c.name == archiveBinaryPath {
				return ExtractedBinary{Name: path.Base(c.name), Bytes: c.body}, nil
			}
		}
		return ExtractedBinary{}, fmt.Errorf("archive_binary_path %q not found among %d ELF entries", archiveBinaryPath, len(elfCandidates))
	}

	if len(elfCandidates) > 1 {
		names := make([]string, 0, len(elfCandidates))
		for _, c := range elfCandidates {
			names = append(names, c.name)
		}
		return ExtractedBinary{}, fmt.Errorf("archive contains multiple ELF binaries (%s); supply archive_binary_path to disambiguate", strings.Join(names, ", "))
	}
	return ExtractedBinary{Name: path.Base(elfCandidates[0].name), Bytes: elfCandidates[0].body}, nil
}

// archiveEntry holds one file's path + bytes pulled out of an archive. Only
// regular files survive; directories, symlinks, and irregular entries are
// dropped (we never execute a symlink target, and the sandbox bind-mount is
// read-only so symlink resolution would be misleading).
type archiveEntry struct {
	name string
	body []byte
}

// scanArchive returns every regular file in the archive. Per-entry size is
// capped at DefaultMaxBinaryBytes so a 4GB entry inside a small archive
// can't blow up memory; the cap is intentionally the same as the upload cap
// because the operator can't usefully store a binary larger than that.
func scanArchive(data []byte, kind ArchiveKind) ([]archiveEntry, error) {
	switch kind {
	case KindZip:
		return scanZip(data)
	case KindTarGz:
		gz, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil, fmt.Errorf("decompress gzip: %w", err)
		}
		defer func() { _ = gz.Close() }()
		return scanTar(gz)
	case KindTar:
		return scanTar(bytes.NewReader(data))
	default:
		return nil, fmt.Errorf("unsupported archive kind %d", kind)
	}
}

func scanZip(data []byte) ([]archiveEntry, error) {
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("open zip: %w", err)
	}
	out := make([]archiveEntry, 0, len(r.File))
	for _, f := range r.File {
		if f.FileInfo().IsDir() {
			continue
		}
		// Refuse path traversal — a malicious archive entry of
		// "../../etc/passwd" is otherwise stored verbatim as the entry
		// "name" and could leak through any caller that joins it onto
		// a host path. We don't materialize archive entries onto disk
		// here (everything lives in memory), but archive_binary_path
		// matching uses this name so we keep it sanitized.
		if strings.Contains(f.Name, "..") {
			continue
		}
		if int64(f.UncompressedSize64) > DefaultMaxBinaryBytes { //nolint:gosec // uncompressed size is bounded by the cap check
			continue
		}
		body, err := readZipEntry(f)
		if err != nil {
			return nil, fmt.Errorf("read zip entry %q: %w", f.Name, err)
		}
		out = append(out, archiveEntry{name: filepath.ToSlash(f.Name), body: body})
	}
	return out, nil
}

func readZipEntry(f *zip.File) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer func() { _ = rc.Close() }()
	// Cap reads at DefaultMaxBinaryBytes to keep a single entry from
	// dominating memory regardless of what the header claimed.
	return io.ReadAll(io.LimitReader(rc, DefaultMaxBinaryBytes+1))
}

func scanTar(r io.Reader) ([]archiveEntry, error) {
	tr := tar.NewReader(r)
	out := make([]archiveEntry, 0, 8)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read tar: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg && hdr.Typeflag != tar.TypeRegA { //nolint:staticcheck // TypeRegA is the legacy "regular file" flag, still valid in old archives
			continue
		}
		if strings.Contains(hdr.Name, "..") {
			continue
		}
		if hdr.Size > DefaultMaxBinaryBytes {
			continue
		}
		body, err := io.ReadAll(io.LimitReader(tr, DefaultMaxBinaryBytes+1))
		if err != nil {
			return nil, fmt.Errorf("read tar entry %q: %w", hdr.Name, err)
		}
		out = append(out, archiveEntry{name: filepath.ToSlash(hdr.Name), body: body})
	}
	return out, nil
}
