package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"

	"github.com/1broseidon/prism/internal/binstore"
	"github.com/1broseidon/prism/internal/openapi"
)

// binaryUploadLimit caps multipart uploads. Mirrors the binstore per-binary
// cap; the upload handler rejects oversize bodies before any extraction so a
// 4GB tarball can't push the gateway into memory pressure.
const binaryUploadLimit int64 = 64 * 1024 * 1024

// BinaryStore is the interface admin uses to persist uploaded binaries.
// Production wiring uses *binstore.Store; tests can fake it.
type BinaryStore interface {
	Put(name string, data []byte) (binstore.Entry, error)
	Stat(hash string) (binstore.Entry, error)
}

// binaryFetcher is the SSRF-guarded URL fetcher used for the
// POST /binaries/fetch path. It reuses the OpenAPI fetcher's guard since the
// concerns are identical: refuse loopback / RFC1918 / link-local, follow
// redirects with the guard re-applied, cap the response body at the upload
// limit.
type binaryFetcher interface {
	Fetch(ctx context.Context, rawURL string) ([]byte, error)
}

// binaryFetcherFactory lets tests inject a non-SSRF fetcher. Defaults to a
// fresh openapi.NewFetcher with a 64MB cap so the binary path doesn't share
// MaxBytes with the (smaller) OpenAPI spec cap.
type binaryFetcherFactory func() binaryFetcher

func defaultBinaryFetcherFactory() binaryFetcher {
	return openapi.NewFetcher(openapi.FetcherConfig{MaxBytes: binaryUploadLimit})
}

// BinaryUploadResponse is the wire shape returned by POST /binaries/upload
// and POST /binaries/fetch. detected_binary_path is the archive-relative
// path that was selected (or empty when the upload was a raw ELF).
type BinaryUploadResponse struct {
	Hash               string `json:"hash"`
	Name               string `json:"name"`
	Size               int64  `json:"size"`
	DetectedBinaryPath string `json:"detected_binary_path,omitempty"`
	Source             string `json:"source"` // "upload" or "url"
	SourceURL          string `json:"source_url,omitempty"`
	ArchiveBinaryPath  string `json:"archive_binary_path,omitempty"`
}

// BinaryFetchRequest is the body for POST /binaries/fetch.
type BinaryFetchRequest struct {
	URL               string `json:"url"`
	ArchiveBinaryPath string `json:"archive_binary_path,omitempty"`
}

// SetBinaryStore wires the content-addressed store the admin handlers use.
// SetBinaryStore is optional — when unset the binary endpoints respond 503.
func (a *API) SetBinaryStore(s BinaryStore) {
	a.binaryStore = s
}

// SetBinaryFetcher overrides the SSRF-guarded fetcher (test hook).
func (a *API) SetBinaryFetcher(f binaryFetcherFactory) {
	a.binaryFetcher = f
}

// handleBinaryUpload accepts multipart POSTs. The first non-empty file part
// is consumed as the upload body; extra parts are ignored. archive_binary_path
// may be supplied as a regular form field for archives with multiple ELFs.
func (a *API) handleBinaryUpload(w http.ResponseWriter, r *http.Request) {
	if a.binaryStore == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "binary store not configured"})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, binaryUploadLimit+1024) // small slack for multipart framing
	if err := r.ParseMultipartForm(binaryUploadLimit + 1024); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid multipart body: " + err.Error()})
		return
	}
	defer func() {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
	}()
	file, header, err := r.FormFile("file")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing 'file' form part: " + err.Error()})
		return
	}
	defer func() { _ = file.Close() }()
	body, err := io.ReadAll(io.LimitReader(file, binaryUploadLimit+1))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "read upload body: " + err.Error()})
		return
	}
	if int64(len(body)) > binaryUploadLimit {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": fmt.Sprintf("upload exceeds %d byte limit", binaryUploadLimit)})
		return
	}
	archivePath := strings.TrimSpace(r.FormValue("archive_binary_path"))
	fallbackName := pickUploadFallbackName(header.Filename)
	entry, detected, err := a.ingestBinary(body, fallbackName, archivePath)
	if err != nil {
		writeJSON(w, errorStatusForBinary(err), map[string]string{"error": err.Error()})
		return
	}
	resp := BinaryUploadResponse{
		Hash:               entry.Hash,
		Name:               entry.Name,
		Size:               entry.Size,
		DetectedBinaryPath: detected,
		Source:             "upload",
		ArchiveBinaryPath:  archivePath,
	}
	writeJSON(w, http.StatusCreated, resp)
}

// handleBinaryFetch downloads a URL through the SSRF guard and ingests the
// result the same way handleBinaryUpload does. Redirect handling and the
// 64MB cap live in the openapi fetcher.
func (a *API) handleBinaryFetch(w http.ResponseWriter, r *http.Request) {
	if a.binaryStore == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "binary store not configured"})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4*1024)
	var req BinaryFetchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	rawURL := strings.TrimSpace(req.URL)
	if rawURL == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "url is required"})
		return
	}
	factory := a.binaryFetcher
	if factory == nil {
		factory = defaultBinaryFetcherFactory
	}
	body, err := factory().Fetch(r.Context(), rawURL)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	fallbackName := pickURLFallbackName(rawURL)
	entry, detected, err := a.ingestBinary(body, fallbackName, req.ArchiveBinaryPath)
	if err != nil {
		writeJSON(w, errorStatusForBinary(err), map[string]string{"error": err.Error()})
		return
	}
	resp := BinaryUploadResponse{
		Hash:               entry.Hash,
		Name:               entry.Name,
		Size:               entry.Size,
		DetectedBinaryPath: detected,
		Source:             "url",
		SourceURL:          rawURL,
		ArchiveBinaryPath:  req.ArchiveBinaryPath,
	}
	writeJSON(w, http.StatusCreated, resp)
}

// handleBinaryGet returns metadata for a stored hash. Useful for the UI to
// confirm an entry survives backend restarts.
func (a *API) handleBinaryGet(w http.ResponseWriter, r *http.Request) {
	if a.binaryStore == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "binary store not configured"})
		return
	}
	hash := strings.TrimPrefix(r.URL.Path, "/binaries/")
	if !isHexHash(hash) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid hash"})
		return
	}
	entry, err := a.binaryStore.Stat(hash)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, BinaryUploadResponse{
		Hash: entry.Hash,
		Name: entry.Name,
		Size: entry.Size,
	})
}

// ingestBinary extracts a single ELF binary out of the supplied bytes
// (treating archives as containers, raw ELFs as themselves), then stores it
// in the binstore. Returns the resulting entry plus the archive-relative
// path that was selected (empty for raw uploads).
func (a *API) ingestBinary(body []byte, fallbackName, archivePath string) (binstore.Entry, string, error) {
	if len(body) == 0 {
		return binstore.Entry{}, "", errors.New("upload body is empty")
	}
	extracted, err := binstore.ExtractBinary(body, archivePath, fallbackName)
	if err != nil {
		return binstore.Entry{}, "", err
	}
	entry, err := a.binaryStore.Put(extracted.Name, extracted.Bytes)
	if err != nil {
		return binstore.Entry{}, "", err
	}
	detected := archivePath
	if detected == "" && binstore.DetectArchiveKind(body) != binstore.KindUnknown {
		// For single-binary archives the operator didn't supply a path —
		// surface the picked entry name so the UI can show "we used X".
		detected = extracted.Name
	}
	return entry, detected, nil
}

// pickUploadFallbackName turns a multipart filename into a binstore name.
// Archives use the extracted entry's basename instead, so this only kicks in
// when the upload turns out to be a raw ELF.
func pickUploadFallbackName(filename string) string {
	name := path.Base(strings.TrimSpace(filename))
	switch name {
	case "", ".", "/":
		return "binary"
	}
	// Strip common archive suffixes — operator uploads "cymbal-linux-amd64.tar.gz"
	// containing "cymbal"; if extraction fell back to the upload filename we
	// don't want the suffix tagging along.
	for _, suffix := range []string{".tar.gz", ".tgz", ".tar", ".zip"} {
		if strings.HasSuffix(strings.ToLower(name), suffix) {
			name = name[:len(name)-len(suffix)]
			break
		}
	}
	if name == "" {
		return "binary"
	}
	return name
}

// pickURLFallbackName extracts a name from the URL path, applying the same
// suffix-stripping as the upload path.
func pickURLFallbackName(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Path == "" {
		return "binary"
	}
	return pickUploadFallbackName(path.Base(u.Path))
}

// errorStatusForBinary maps binstore errors to HTTP statuses. Validation
// errors (bad ELF, missing archive entry) are 400; storage caps are 413;
// everything else is 500 so the operator can see a bug surface clearly.
func errorStatusForBinary(err error) int {
	switch {
	case errors.Is(err, binstore.ErrTooLarge), errors.Is(err, binstore.ErrCapacity):
		return http.StatusRequestEntityTooLarge
	case errors.Is(err, binstore.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, binstore.ErrInvalidName):
		return http.StatusBadRequest
	}
	msg := err.Error()
	if strings.Contains(msg, "ssrf guard") || strings.Contains(msg, "fetch") ||
		strings.Contains(msg, "ELF") || strings.Contains(msg, "archive") ||
		strings.Contains(msg, "binary") {
		return http.StatusBadRequest
	}
	return http.StatusInternalServerError
}

// isHexHash reports whether s is a 64-char lowercase hex string. Inline copy
// of binstore.isHexSHA256 so the admin layer doesn't need to bounce through
// the binstore for path validation.
func isHexHash(s string) bool {
	if len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// shellSplit tokenizes input the way a Bourne shell would for the common
// case: whitespace splits arguments, single quotes are literal, double
// quotes allow backslash escaping. The MCP-command field accepts at most a
// few tokens (binary + a couple of subcommands), so a full POSIX parser
// would be overkill. Edge cases (env-var expansion, command substitution,
// here-strings) are not supported on purpose — they'd be a foot-gun in this
// context.
//
//nolint:gocyclo // small state machine is clearer as a single function.
func shellSplit(input string) ([]string, error) {
	var tokens []string
	var current strings.Builder
	inToken := false
	inSingle := false
	inDouble := false
	escape := false
	flush := func() {
		if inToken {
			tokens = append(tokens, current.String())
			current.Reset()
			inToken = false
		}
	}
	for _, r := range input {
		if escape {
			current.WriteRune(r)
			escape = false
			inToken = true
			continue
		}
		if inSingle {
			if r == '\'' {
				inSingle = false
				continue
			}
			current.WriteRune(r)
			continue
		}
		if inDouble {
			if r == '\\' {
				escape = true
				continue
			}
			if r == '"' {
				inDouble = false
				continue
			}
			current.WriteRune(r)
			continue
		}
		switch r {
		case '\'':
			inSingle = true
			inToken = true
		case '"':
			inDouble = true
			inToken = true
		case '\\':
			escape = true
			inToken = true
		case ' ', '\t', '\n':
			flush()
		default:
			current.WriteRune(r)
			inToken = true
		}
	}
	if inSingle || inDouble {
		return nil, errors.New("unterminated quoted string")
	}
	if escape {
		return nil, errors.New("trailing backslash")
	}
	flush()
	return tokens, nil
}

// ParseBinaryCommand splits the operator-supplied MCP command field into
// argv. The first token is treated as informational — it's the operator's
// display name for the binary, not an executable path. The actual binary
// path is set by the gateway from the binstore mount target.
//
// Examples:
//
//	""               -> ("", nil)
//	"mcp"            -> ("mcp", nil)        — single token: just args
//	"recoil mcp"     -> ("recoil", ["mcp"])
//	"recoil mcp serve" -> ("recoil", ["mcp","serve"])
//
// The first-token interpretation matches the operator's mental model:
// they paste "recoil mcp serve" because that's what they'd type locally,
// and prism strips "recoil" (the binary name) keeping "mcp serve" as args.
// When only one token is supplied we treat it as an arg, not a name, so
// "mcp" doesn't end up as the name with zero args.
func ParseBinaryCommand(input string) (firstToken string, args []string, err error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return "", nil, nil
	}
	tokens, err := shellSplit(input)
	if err != nil {
		return "", nil, err
	}
	if len(tokens) == 0 {
		return "", nil, nil
	}
	if len(tokens) == 1 {
		// "mcp" -> args=["mcp"], no first token. Matches the contract: a
		// single token is interpreted as a subcommand argument, not the
		// binary name.
		return "", append([]string(nil), tokens...), nil
	}
	return tokens[0], append([]string(nil), tokens[1:]...), nil
}
