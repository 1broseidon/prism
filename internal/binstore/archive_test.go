package binstore

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"strings"
	"testing"
)

// buildELF synthesizes the minimum 20 bytes ValidateELF inspects. Tests can
// flip class/machine/endian to assert each rejection path. The remaining
// bytes are zero — that's enough for our header sniff and keeps the fixture
// small (real ELFs are 64+ bytes).
func buildELF(class64 bool, machine uint16, littleEndian bool) []byte {
	header := make([]byte, 64)
	copy(header[:4], []byte{0x7f, 'E', 'L', 'F'})
	if class64 {
		header[4] = 2
	} else {
		header[4] = 1
	}
	if littleEndian {
		header[5] = 1
		binary.LittleEndian.PutUint16(header[18:20], machine)
	} else {
		header[5] = 2
		binary.BigEndian.PutUint16(header[18:20], machine)
	}
	return header
}

func TestDetectArchiveKind(t *testing.T) {
	cases := []struct {
		name string
		data []byte
		want ArchiveKind
	}{
		{"zip", []byte{'P', 'K', 0x03, 0x04, 0, 0, 0, 0}, KindZip},
		{"gzip", []byte{0x1f, 0x8b, 0, 0, 0, 0, 0, 0, 0, 0}, KindTarGz},
		{"tar", makeTarMagic(), KindTar},
		{"plain", buildELF(true, elfMachineX86, true), KindUnknown},
		{"empty", []byte{}, KindUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := DetectArchiveKind(tc.data)
			if got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}

func makeTarMagic() []byte {
	buf := make([]byte, 264)
	copy(buf[257:], []byte("ustar\x00"))
	return buf
}

func TestValidateELFAcceptsAmd64(t *testing.T) {
	good := buildELF(true, elfMachineX86, true)
	info, err := ValidateELF(good)
	if err != nil {
		t.Fatalf("expected accept, got %v", err)
	}
	if !info.Class64 || info.Architecture != "x86_64" {
		t.Fatalf("unexpected info: %+v", info)
	}
}

func TestValidateELFRejections(t *testing.T) {
	cases := []struct {
		name    string
		payload []byte
		wantSub string
	}{
		{"too short", []byte("\x7fELF"), "too short"},
		{"bad magic", []byte("ABCD" + strings.Repeat("\x00", 60)), "bad magic"},
		{"32-bit", buildELF(false, elfMachineX86, true), "32-bit"},
		{"aarch64", buildELF(true, elfMachineARM, true), "aarch64"},
		{"riscv", buildELF(true, elfMachineRISC, true), "riscv"},
		{"big endian", buildELF(true, elfMachineX86, false), "big-endian"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ValidateELF(tc.payload)
			if err == nil || !strings.Contains(err.Error(), tc.wantSub) {
				t.Fatalf("expected error containing %q, got %v", tc.wantSub, err)
			}
		})
	}
}

func TestExtractBinaryRawELF(t *testing.T) {
	elf := buildELF(true, elfMachineX86, true)
	got, err := ExtractBinary(elf, "", "cymbal")
	if err != nil {
		t.Fatalf("ExtractBinary: %v", err)
	}
	if got.Name != "cymbal" || !bytes.Equal(got.Bytes, elf) {
		t.Fatalf("unexpected result: %+v", got)
	}
}

func TestExtractBinaryRejectsNonELF(t *testing.T) {
	_, err := ExtractBinary([]byte("not an elf"), "", "x")
	if err == nil {
		t.Fatalf("expected error for non-ELF input")
	}
}

func buildZip(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("zip Create: %v", err)
		}
		if _, err := w.Write(body); err != nil {
			t.Fatalf("zip Write: %v", err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip Close: %v", err)
	}
	return buf.Bytes()
}

func buildTarGz(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		hdr := &tar.Header{Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("tar WriteHeader: %v", err)
		}
		if _, err := tw.Write(body); err != nil {
			t.Fatalf("tar Write: %v", err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar Close: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gzip Close: %v", err)
	}
	return buf.Bytes()
}

func TestExtractBinaryZipSingleELF(t *testing.T) {
	elf := buildELF(true, elfMachineX86, true)
	archive := buildZip(t, map[string][]byte{
		"README.md": []byte("# hello"),
		"recoil":    elf,
	})
	got, err := ExtractBinary(archive, "", "")
	if err != nil {
		t.Fatalf("ExtractBinary: %v", err)
	}
	if got.Name != "recoil" || !bytes.Equal(got.Bytes, elf) {
		t.Fatalf("unexpected: %+v", got)
	}
}

func TestExtractBinaryTarGzSingleELF(t *testing.T) {
	elf := buildELF(true, elfMachineX86, true)
	archive := buildTarGz(t, map[string][]byte{
		"docs/notes.txt": []byte("hi"),
		"bin/cymbal":     elf,
	})
	got, err := ExtractBinary(archive, "", "")
	if err != nil {
		t.Fatalf("ExtractBinary: %v", err)
	}
	if got.Name != "cymbal" {
		t.Fatalf("expected leaf name 'cymbal', got %q", got.Name)
	}
}

func TestExtractBinaryArchiveMultipleELFsRequirePath(t *testing.T) {
	elf := buildELF(true, elfMachineX86, true)
	archive := buildZip(t, map[string][]byte{
		"cymbal":        elf,
		"cymbal-helper": elf,
	})
	if _, err := ExtractBinary(archive, "", ""); err == nil || !strings.Contains(err.Error(), "multiple ELF binaries") {
		t.Fatalf("expected multi-binary error, got %v", err)
	}
	got, err := ExtractBinary(archive, "cymbal-helper", "")
	if err != nil {
		t.Fatalf("with path: %v", err)
	}
	if got.Name != "cymbal-helper" {
		t.Fatalf("expected cymbal-helper, got %q", got.Name)
	}
}

func TestExtractBinaryArchiveNoELF(t *testing.T) {
	archive := buildZip(t, map[string][]byte{
		"README.md": []byte("not an elf"),
	})
	if _, err := ExtractBinary(archive, "", ""); err == nil || !strings.Contains(err.Error(), "no linux/amd64 ELF") {
		t.Fatalf("expected no-ELF error, got %v", err)
	}
}

func TestExtractBinaryArchiveRejectsARM(t *testing.T) {
	armELF := buildELF(true, elfMachineARM, true)
	archive := buildZip(t, map[string][]byte{
		"recoil": armELF,
	})
	// aarch64 binaries don't pass ValidateELF so they're skipped — caller
	// sees "no linux/amd64 ELF". This matches the contract: only the raw
	// upload path surfaces the per-arch error message, archives surface
	// "no ELF" because we can't tell which entry the operator intended.
	if _, err := ExtractBinary(archive, "", ""); err == nil {
		t.Fatalf("expected error, got nil")
	}
}

func TestExtractBinaryArchiveBinaryPathNotFound(t *testing.T) {
	elf := buildELF(true, elfMachineX86, true)
	archive := buildZip(t, map[string][]byte{
		"cymbal":        elf,
		"cymbal-helper": elf,
	})
	_, err := ExtractBinary(archive, "missing", "")
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected not-found error, got %v", err)
	}
}
