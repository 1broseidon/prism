//go:build integration

package integration

import (
	"net/http"
	"testing"

	"github.com/1broseidon/prism/internal/identity"
)

func TestE2E_Identity_Rename(t *testing.T) {
	s := newE2ESuite(t)
	idMgr := identity.New(s.kv)
	s.adminAPI.SetIdentity(idMgr)

	var allocated identity.Entity
	s.adminJSON(http.MethodPost, "/identity",
		map[string]string{"kind": "group", "display_name": "engineering"},
		http.StatusCreated, &allocated)
	if allocated.ID == "" {
		t.Fatal("no ID returned from allocate")
	}

	var renamed identity.Entity
	s.adminJSON(http.MethodPut, "/identity/"+allocated.ID+"/display-name",
		map[string]string{"display_name": "engineering-renamed"},
		http.StatusOK, &renamed)
	if renamed.DisplayName != "engineering-renamed" {
		t.Fatalf("display_name after rename = %q, want %q", renamed.DisplayName, "engineering-renamed")
	}

	var second identity.Entity
	s.adminJSON(http.MethodPost, "/identity",
		map[string]string{"kind": "group", "display_name": "platform"},
		http.StatusCreated, &second)

	var conflictBody map[string]string
	s.adminJSON(http.MethodPut, "/identity/"+second.ID+"/display-name",
		map[string]string{"display_name": "engineering-renamed"},
		http.StatusConflict, &conflictBody)
	if conflictBody["error"] != "display_name_in_use" {
		t.Fatalf("conflict body = %+v", conflictBody)
	}
}
