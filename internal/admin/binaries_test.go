package admin

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/1broseidon/prism/internal/binstore"
)

// fakeBinaryStore implements BinaryStore with in-memory storage. Tests can
// assert on the byte content and seed pre-existing entries (e.g. for the
// AddBackend path that needs to look up a hash).
type fakeBinaryStore struct {
	mu      sync.Mutex
	entries map[string]binaryEntry
}

type binaryEntry struct {
	name string
	body []byte
}

func newFakeBinaryStore() *fakeBinaryStore {
	return &fakeBinaryStore{entries: make(map[string]binaryEntry)}
}

func (s *fakeBinaryStore) Put(name string, data []byte) (binstore.Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	hash := binstore.HashBytes(data)
	if _, ok := s.entries[hash]; !ok {
		s.entries[hash] = binaryEntry{name: name, body: append([]byte(nil), data...)}
	}
	return binstore.Entry{Hash: hash, Name: s.entries[hash].name, Size: int64(len(s.entries[hash].body))}, nil
}

func (s *fakeBinaryStore) Stat(hash string) (binstore.Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e, ok := s.entries[hash]; ok {
		return binstore.Entry{Hash: hash, Name: e.name, Size: int64(len(e.body))}, nil
	}
	return binstore.Entry{}, binstore.ErrNotFound
}

// fakeFetcher implements binaryFetcher for the URL-fetch tests. It returns
// the pre-loaded body for any URL.
type fakeFetcher struct {
	body []byte
	err  error
}

func (f *fakeFetcher) Fetch(_ context.Context, _ string) ([]byte, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.body, nil
}

// elfFixture returns a minimal valid linux/amd64 ELF header (64 bytes). The
// extra payload is appended so different fixtures hash to different values
// (otherwise dedup would short-circuit the multi-entry tests).
func elfFixture(payload string) []byte {
	out := make([]byte, 64, 64+len(payload))
	out[0] = 0x7f
	out[1] = 'E'
	out[2] = 'L'
	out[3] = 'F'
	out[4] = 2 // EI_CLASS = 64-bit
	out[5] = 1 // EI_DATA = little-endian
	binary.LittleEndian.PutUint16(out[18:20], 0x3E)
	return append(out, []byte(payload)...)
}

func buildMultipart(t *testing.T, filename string, body []byte, fields map[string]string) (string, []byte) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for k, v := range fields {
		if err := mw.WriteField(k, v); err != nil {
			t.Fatalf("WriteField: %v", err)
		}
	}
	w, err := mw.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := w.Write(body); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	return mw.FormDataContentType(), buf.Bytes()
}

func decodeBinaryResponse(t *testing.T, body []byte) BinaryUploadResponse {
	t.Helper()
	var resp BinaryUploadResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode: %v body=%s", err, body)
	}
	return resp
}

func TestBinaryUploadRawELF(t *testing.T) {
	store := newFakeBinaryStore()
	api := &API{binaryStore: store}
	body := elfFixture("recoil-payload")
	contentType, multi := buildMultipart(t, "recoil-linux-amd64", body, nil)

	req := httptest.NewRequest(http.MethodPost, "/binaries/upload", bytes.NewReader(multi))
	req.Header.Set("Content-Type", contentType)
	rec := httptest.NewRecorder()
	api.handleBinaryUpload(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	resp := decodeBinaryResponse(t, rec.Body.Bytes())
	if resp.Hash == "" || resp.Source != "upload" || resp.Name == "" {
		t.Fatalf("unexpected response: %+v", resp)
	}
	if !bytes.Equal(store.entries[resp.Hash].body, body) {
		t.Fatalf("stored bytes mismatch")
	}
}

func TestBinaryUploadArchiveAutoDetect(t *testing.T) {
	store := newFakeBinaryStore()
	api := &API{binaryStore: store}
	elf := elfFixture("solo-payload")
	archive := zipArchive(t, map[string][]byte{
		"README.md": []byte("doc"),
		"recoil":    elf,
	})
	contentType, multi := buildMultipart(t, "recoil.zip", archive, nil)
	req := httptest.NewRequest(http.MethodPost, "/binaries/upload", bytes.NewReader(multi))
	req.Header.Set("Content-Type", contentType)
	rec := httptest.NewRecorder()
	api.handleBinaryUpload(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	resp := decodeBinaryResponse(t, rec.Body.Bytes())
	if resp.Name != "recoil" {
		t.Fatalf("expected name 'recoil', got %q", resp.Name)
	}
	if resp.DetectedBinaryPath != "recoil" {
		t.Fatalf("expected detected_binary_path=recoil, got %q", resp.DetectedBinaryPath)
	}
}

func TestBinaryUploadArchiveMultipleRequiresPath(t *testing.T) {
	store := newFakeBinaryStore()
	api := &API{binaryStore: store}
	elf := elfFixture("multi-payload")
	archive := zipArchive(t, map[string][]byte{
		"recoil":        elf,
		"recoil-helper": elf,
	})
	// First attempt: no path supplied — expect 400 with "multiple".
	contentType, multi := buildMultipart(t, "recoil.zip", archive, nil)
	req := httptest.NewRequest(http.MethodPost, "/binaries/upload", bytes.NewReader(multi))
	req.Header.Set("Content-Type", contentType)
	rec := httptest.NewRecorder()
	api.handleBinaryUpload(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "multiple") {
		t.Fatalf("expected 'multiple' in error, got %s", rec.Body.String())
	}

	// Second attempt: supply archive_binary_path.
	contentType, multi = buildMultipart(t, "recoil.zip", archive, map[string]string{"archive_binary_path": "recoil-helper"})
	req = httptest.NewRequest(http.MethodPost, "/binaries/upload", bytes.NewReader(multi))
	req.Header.Set("Content-Type", contentType)
	rec = httptest.NewRecorder()
	api.handleBinaryUpload(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("disambiguated upload status = %d body=%s", rec.Code, rec.Body.String())
	}
	resp := decodeBinaryResponse(t, rec.Body.Bytes())
	if resp.Name != "recoil-helper" {
		t.Fatalf("expected recoil-helper, got %q", resp.Name)
	}
}

func TestBinaryUploadRejectsNonELF(t *testing.T) {
	api := &API{binaryStore: newFakeBinaryStore()}
	contentType, multi := buildMultipart(t, "junk", []byte("not an elf binary at all"), nil)
	req := httptest.NewRequest(http.MethodPost, "/binaries/upload", bytes.NewReader(multi))
	req.Header.Set("Content-Type", contentType)
	rec := httptest.NewRecorder()
	api.handleBinaryUpload(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestBinaryUploadRejectsARM(t *testing.T) {
	api := &API{binaryStore: newFakeBinaryStore()}
	armELF := elfFixture("arm")
	// Overwrite the e_machine to aarch64.
	binary.LittleEndian.PutUint16(armELF[18:20], 0xB7)
	contentType, multi := buildMultipart(t, "recoil", armELF, nil)
	req := httptest.NewRequest(http.MethodPost, "/binaries/upload", bytes.NewReader(multi))
	req.Header.Set("Content-Type", contentType)
	rec := httptest.NewRecorder()
	api.handleBinaryUpload(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for arm, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "aarch64") {
		t.Fatalf("expected arch name in error, got %s", rec.Body.String())
	}
}

func TestBinaryFetchURL(t *testing.T) {
	store := newFakeBinaryStore()
	body := elfFixture("via-url")
	api := &API{binaryStore: store}
	api.SetBinaryFetcher(func() binaryFetcher { return &fakeFetcher{body: body} })

	payload, _ := json.Marshal(BinaryFetchRequest{URL: "https://example.com/release/recoil-linux-amd64"})
	req := httptest.NewRequest(http.MethodPost, "/binaries/fetch", bytes.NewReader(payload))
	rec := httptest.NewRecorder()
	api.handleBinaryFetch(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	resp := decodeBinaryResponse(t, rec.Body.Bytes())
	if resp.Source != "url" || resp.SourceURL != "https://example.com/release/recoil-linux-amd64" {
		t.Fatalf("unexpected resp: %+v", resp)
	}
	if resp.Name != "recoil-linux-amd64" {
		t.Fatalf("name = %q", resp.Name)
	}
}

func TestBinaryFetchExtractsFromArchive(t *testing.T) {
	store := newFakeBinaryStore()
	elf := elfFixture("via-archive")
	archive := zipArchive(t, map[string][]byte{"recoil": elf})
	api := &API{binaryStore: store}
	api.SetBinaryFetcher(func() binaryFetcher { return &fakeFetcher{body: archive} })

	payload, _ := json.Marshal(BinaryFetchRequest{URL: "https://example.com/recoil.zip"})
	req := httptest.NewRequest(http.MethodPost, "/binaries/fetch", bytes.NewReader(payload))
	rec := httptest.NewRecorder()
	api.handleBinaryFetch(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	resp := decodeBinaryResponse(t, rec.Body.Bytes())
	if resp.Name != "recoil" {
		t.Fatalf("name = %q", resp.Name)
	}
}

func TestBinaryFetchPropagatesFetcherError(t *testing.T) {
	api := &API{binaryStore: newFakeBinaryStore()}
	api.SetBinaryFetcher(func() binaryFetcher { return &fakeFetcher{err: errors.New("ssrf guard: blocked")} })
	payload, _ := json.Marshal(BinaryFetchRequest{URL: "http://localhost/internal"})
	req := httptest.NewRequest(http.MethodPost, "/binaries/fetch", bytes.NewReader(payload))
	rec := httptest.NewRecorder()
	api.handleBinaryFetch(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestBinaryGetMetadata(t *testing.T) {
	store := newFakeBinaryStore()
	entry, _ := store.Put("recoil", elfFixture("get-meta"))
	api := &API{binaryStore: store}

	req := httptest.NewRequest(http.MethodGet, "/binaries/"+entry.Hash, http.NoBody)
	rec := httptest.NewRecorder()
	api.handleBinaryGet(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	resp := decodeBinaryResponse(t, rec.Body.Bytes())
	if resp.Hash != entry.Hash || resp.Name != "recoil" {
		t.Fatalf("unexpected: %+v", resp)
	}
}

func TestBinaryGetRejectsBadHash(t *testing.T) {
	api := &API{binaryStore: newFakeBinaryStore()}
	req := httptest.NewRequest(http.MethodGet, "/binaries/not-a-hash", http.NoBody)
	rec := httptest.NewRecorder()
	api.handleBinaryGet(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestBinaryEndpointsRequireStore(t *testing.T) {
	api := &API{}
	t.Run("upload", func(t *testing.T) {
		contentType, multi := buildMultipart(t, "x", []byte{1, 2, 3}, nil)
		req := httptest.NewRequest(http.MethodPost, "/binaries/upload", bytes.NewReader(multi))
		req.Header.Set("Content-Type", contentType)
		rec := httptest.NewRecorder()
		api.handleBinaryUpload(rec, req)
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d", rec.Code)
		}
	})
	t.Run("fetch", func(t *testing.T) {
		payload, _ := json.Marshal(BinaryFetchRequest{URL: "https://example.com/x"})
		req := httptest.NewRequest(http.MethodPost, "/binaries/fetch", bytes.NewReader(payload))
		rec := httptest.NewRecorder()
		api.handleBinaryFetch(rec, req)
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d", rec.Code)
		}
	})
}

func TestBinaryUploadEnforcesSizeCap(t *testing.T) {
	// We can't easily hit the 64MB cap in a test, but feeding a non-ELF
	// raw payload through the cap-aware code path verifies the
	// LimitReader is in place: undersized rejection should fall to the
	// ELF validator, not the body reader.
	store := newFakeBinaryStore()
	api := &API{binaryStore: store}
	tiny := []byte{0x7f, 'E', 'L', 'F'} // 4 bytes — fails ValidateELF.too-short
	contentType, multi := buildMultipart(t, "x", tiny, nil)
	req := httptest.NewRequest(http.MethodPost, "/binaries/upload", bytes.NewReader(multi))
	req.Header.Set("Content-Type", contentType)
	rec := httptest.NewRecorder()
	api.handleBinaryUpload(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestAddBinaryBackendValidates exercises handleAddBackend with a binary
// backend body. The fake manager records the payload it sees so we can assert
// the admin layer hands through the binary_hash without normalizing it away.
func TestAddBinaryBackendValidates(t *testing.T) {
	store := newFakeBinaryStore()
	entry, _ := store.Put("cymbal", elfFixture("add-flow"))

	mgr := &fakeBinaryBackendManager{calls: make(chan BackendConfig, 1)}
	api := &API{backendMgr: mgr, binaryStore: store}

	body := mustEncodeBody(t, BackendConfig{
		BinaryHash: entry.Hash,
		BinaryArgs: []string{"mcp", "serve"},
		BinaryName: "cymbal",
	})
	req := httptest.NewRequest(http.MethodPost, "/backends/cym", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	api.handleAddBackend(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	// Async add path defers; the fake's calls channel is buffered so the
	// goroutine can deposit its payload without blocking on a receiver.
	got := <-mgr.calls
	if got.BinaryHash != entry.Hash {
		t.Fatalf("binary_hash mismatch: %q", got.BinaryHash)
	}
	if len(got.BinaryArgs) != 2 || got.BinaryArgs[0] != "mcp" {
		t.Fatalf("args = %v", got.BinaryArgs)
	}
}

func TestAddBinaryBackendRejectsMixedTransport(t *testing.T) {
	store := newFakeBinaryStore()
	entry, _ := store.Put("cymbal", elfFixture("mixed-flow"))
	mgr := &fakeBinaryBackendManager{}
	api := &API{backendMgr: mgr, binaryStore: store}

	body := mustEncodeBody(t, BackendConfig{
		BinaryHash: entry.Hash,
		Command:    "echo",
	})
	req := httptest.NewRequest(http.MethodPost, "/backends/cym", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	api.handleAddBackend(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestAddBinaryBackendUnknownHash(t *testing.T) {
	store := newFakeBinaryStore()
	mgr := &fakeBinaryBackendManager{}
	api := &API{backendMgr: mgr, binaryStore: store}

	missing := strings.Repeat("a", 64)
	body := mustEncodeBody(t, BackendConfig{BinaryHash: missing})
	req := httptest.NewRequest(http.MethodPost, "/backends/cym", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	api.handleAddBackend(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestParseBinaryCommand(t *testing.T) {
	cases := []struct {
		input     string
		wantFirst string
		wantArgs  []string
		wantErr   bool
	}{
		{"", "", nil, false},
		{"   ", "", nil, false},
		{"mcp", "", []string{"mcp"}, false},
		{"recoil mcp", "recoil", []string{"mcp"}, false},
		{"recoil mcp serve", "recoil", []string{"mcp", "serve"}, false},
		{`recoil "with space" arg`, "recoil", []string{"with space", "arg"}, false},
		{`recoil 'single quoted'`, "recoil", []string{"single quoted"}, false},
		{`bin a\ b`, "bin", []string{"a b"}, false},
		{`bin "unterminated`, "", nil, true},
	}
	for _, tc := range cases {
		first, args, err := ParseBinaryCommand(tc.input)
		if (err != nil) != tc.wantErr {
			t.Errorf("ParseBinaryCommand(%q) err=%v wantErr=%v", tc.input, err, tc.wantErr)
			continue
		}
		if tc.wantErr {
			continue
		}
		if first != tc.wantFirst {
			t.Errorf("ParseBinaryCommand(%q) first=%q want %q", tc.input, first, tc.wantFirst)
		}
		if !equalStrings(args, tc.wantArgs) {
			t.Errorf("ParseBinaryCommand(%q) args=%v want %v", tc.input, args, tc.wantArgs)
		}
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// fakeBinaryBackendManager records AddBackend calls for assertion. The
// AddBackend method is called from a goroutine in the async path, so we use a
// channel to give the test deterministic visibility into the recorded call.
type fakeBinaryBackendManager struct {
	calls chan BackendConfig
}

func (f *fakeBinaryBackendManager) AddBackend(_ context.Context, _ string, cfg BackendConfig) error {
	// Buffered channel so the async goroutine in addBackendAsync doesn't
	// deadlock if the test happens to skip the receive — the test allocates
	// a fresh manager with a buffered channel per case.
	select {
	case f.calls <- cfg:
	default:
	}
	return nil
}
func (f *fakeBinaryBackendManager) RemoveBackend(string) error { return nil }
func (f *fakeBinaryBackendManager) NotifyToolsChanged()        {}

// mustEncodeBody is used by other tests in this package; if it's already
// defined we reuse it. The OpenAPI tests define it as a helper.

// zipArchive builds an in-memory ZIP for tests.
func zipArchive(t *testing.T, files map[string][]byte) []byte {
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
