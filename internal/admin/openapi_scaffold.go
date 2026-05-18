package admin

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/1broseidon/prism/internal/openapi"
)

// openAPIScaffoldRequest is the body for POST /openapi/scaffold-from-curl.
// Curl is the raw curl command string as it would be pasted from a terminal.
type openAPIScaffoldRequest struct {
	Curl string `json:"curl"`
}

// openAPIScaffoldResponse carries the generated YAML spec plus any warnings
// the parser/scaffolder accumulated. The UI displays warnings inline so the
// operator knows which curl flags were dropped.
type openAPIScaffoldResponse struct {
	Spec     string   `json:"spec"`
	Warnings []string `json:"warnings,omitempty"`
}

// handleOpenAPIScaffold implements POST /openapi/scaffold-from-curl. The
// endpoint is stateless: it converts a curl string into an OpenAPI 3.1 YAML
// stub the operator can paste into the inline editor.
//
// Auth-gated like the other openapi mutation routes (admin role required when
// admin auth is configured).
func (a *API) handleOpenAPIScaffold(w http.ResponseWriter, r *http.Request) {
	// 32KB cap on the curl input — far more than a sane invocation needs, and
	// small enough that we don't have to worry about adversarial payloads.
	r.Body = http.MaxBytesReader(w, r.Body, openapi.MaxCurlInputBytes+1024)
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("read body: %v", err)})
		return
	}
	var req openAPIScaffoldRequest
	if len(bodyBytes) > 0 {
		if jerr := json.Unmarshal(bodyBytes, &req); jerr != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + jerr.Error()})
			return
		}
	}
	if req.Curl == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "curl is required"})
		return
	}
	if len(req.Curl) > openapi.MaxCurlInputBytes {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": fmt.Sprintf("curl input exceeds %d byte limit", openapi.MaxCurlInputBytes),
		})
		return
	}

	cmd, parseWarnings, err := openapi.ParseCurl(req.Curl)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	spec, scaffoldWarnings, err := openapi.ScaffoldFromCurl(cmd)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	warnings := make([]string, 0, len(parseWarnings)+len(scaffoldWarnings))
	warnings = append(warnings, parseWarnings...)
	warnings = append(warnings, scaffoldWarnings...)
	if warnings == nil {
		// Keep the field omitted in the response when there's nothing to surface.
		warnings = []string{}
	}

	writeJSON(w, http.StatusOK, openAPIScaffoldResponse{
		Spec:     string(spec),
		Warnings: warnings,
	})
}
