//go:build integration

package integration

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/1broseidon/prism/internal/admin"
	"github.com/1broseidon/prism/internal/authserver"
	"github.com/1broseidon/prism/internal/identity"
)

func TestE2E_Identity_URLCompat(t *testing.T) {
	s := newE2ESuite(t)
	idMgr := identity.New(s.kv)
	s.authSrv.SetIdentityDispatcher(idMgr)
	s.adminAPI.SetIdentity(idMgr)

	ent, err := idMgr.Allocate(identity.KindGroup, "engineering")
	if err != nil {
		t.Fatalf("Allocate group: %v", err)
	}
	groupID := ent.ID

	if err := s.authSrv.SetGroup(groupID, &authserver.GroupConfig{Scopes: []string{"fs:read_file"}}); err != nil {
		t.Fatalf("SetGroup: %v", err)
	}

	capSpec := admin.CapabilitySpec{
		Action: admin.ActionSpec{Mode: "tool", Backend: "fs", Tool: "write_file"},
	}
	s.adminJSON(http.MethodPost, "/policy/subjects/groups/"+groupID+"/capabilities", capSpec, http.StatusCreated, nil)

	resp := s.doRawAdmin(http.MethodGet, "/policy/subjects/groups/engineering/capabilities")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMovedPermanently {
		data, _ := io.ReadAll(resp.Body)
		t.Fatalf("want 301, got %d body=%s", resp.StatusCode, data)
	}
	loc := resp.Header.Get("Location")
	if !strings.Contains(loc, groupID) {
		t.Fatalf("Location %q doesn't contain ULID %s", loc, groupID)
	}

	followURL := loc
	if strings.HasPrefix(followURL, "/") {
		followURL = s.adminHTTP.URL + followURL
	}
	followResp, err := http.DefaultClient.Get(followURL)
	if err != nil {
		t.Fatalf("follow redirect: %v", err)
	}
	defer followResp.Body.Close()
	if followResp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(followResp.Body)
		t.Fatalf("follow status=%d want=200 body=%s", followResp.StatusCode, data)
	}

	resp2 := s.doRawAdmin(http.MethodGet, "/policy/subjects/groups/nonexistent/capabilities")
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusNotFound {
		data, _ := io.ReadAll(resp2.Body)
		t.Fatalf("want 404, got %d body=%s", resp2.StatusCode, data)
	}
	var notFound map[string]string
	if err := json.NewDecoder(resp2.Body).Decode(&notFound); err != nil {
		t.Fatalf("decode 404 body: %v", err)
	}
	if notFound["error"] != "identity_not_found" || notFound["kind"] != "group" || notFound["name"] != "nonexistent" {
		t.Fatalf("404 body = %+v", notFound)
	}
}
