package admin

import (
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/1broseidon/prism/internal/identity"
)

type identityURLMatch struct {
	kind    identity.Kind
	id      string
	replace func(string) string
}

// identityCompat redirects legacy display-name keyed identity URLs to their
// canonical ID-keyed form. It must be wrapped inside session/admin auth
// middleware so anonymous requests are challenged before name resolution.
func (a *API) identityCompat(kind identity.Kind, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.identity == nil {
			next.ServeHTTP(w, r)
			return
		}
		match, ok := matchFixedIdentityPath(r.URL.Path, kind)
		if !ok {
			next.ServeHTTP(w, r)
			return
		}
		a.resolveIdentityURL(w, r, next, match)
	})
}

// policySubjectIdentityCompat applies the same URL compatibility rule to
// /policy/subjects/{type}/{id}/capabilities[/...], where the identity kind is
// derived from {type}.
func (a *API) policySubjectIdentityCompat(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.identity == nil {
			next.ServeHTTP(w, r)
			return
		}
		match, ok := matchPolicySubjectIdentityPath(r.URL.Path)
		if !ok {
			next.ServeHTTP(w, r)
			return
		}
		a.resolveIdentityURL(w, r, next, match)
	})
}

func (a *API) resolveIdentityURL(w http.ResponseWriter, r *http.Request, next http.Handler, match identityURLMatch) {
	if match.id == "" || identity.IsULID(match.id) {
		next.ServeHTTP(w, r)
		return
	}
	ent, err := a.identity.ResolveByName(match.kind, match.id)
	if err != nil {
		if !errors.Is(err, identity.ErrNotFound) {
			writeJSON(w, identityErrorStatus(err), map[string]string{"error": err.Error()})
			return
		}
		// Per-kind handling for unknown names:
		//   * Groups + Backends are explicit-create entities — operators
		//     pre-create them, so an unknown name is a real 404.
		//   * Roles are implicit-create (the policy_builder POST mints a
		//     role on the first capability that references it). Falling
		//     through lets that creation flow work; an after-the-fact
		//     redirect to the freshly minted ULID isn't useful.
		if match.kind == identity.KindRole {
			next.ServeHTTP(w, r)
			return
		}
		writeJSON(w, http.StatusNotFound, map[string]string{
			"error": "identity_not_found",
			"kind":  string(match.kind),
			"name":  match.id,
		})
		return
	}
	location := identityCompatLocation(r, match.replace(ent.ID))
	w.Header().Set("Location", location)
	w.WriteHeader(http.StatusMovedPermanently)
}

func matchFixedIdentityPath(path string, kind identity.Kind) (identityURLMatch, bool) {
	var prefixes []string
	switch kind {
	case identity.KindGroup:
		prefixes = []string{"/groups/", "/agents/groups/"}
	case identity.KindRole:
		prefixes = []string{"/agents/roles/"}
	case identity.KindBackend:
		prefixes = []string{"/backends/", "/servers/"}
	default:
		return identityURLMatch{}, false
	}
	for _, prefix := range prefixes {
		if !strings.HasPrefix(path, prefix) {
			continue
		}
		id, suffix, ok := firstPathSegment(strings.TrimPrefix(path, prefix))
		if !ok {
			return identityURLMatch{}, false
		}
		return identityURLMatch{
			kind: kind,
			id:   id,
			replace: func(newID string) string {
				return prefix + newID + suffix
			},
		}, true
	}
	return identityURLMatch{}, false
}

func matchPolicySubjectIdentityPath(path string) (identityURLMatch, bool) {
	const prefix = "/policy/subjects/"
	if !strings.HasPrefix(path, prefix) {
		return identityURLMatch{}, false
	}
	subjectType, rest, ok := firstPathSegment(strings.TrimPrefix(path, prefix))
	if !ok {
		return identityURLMatch{}, false
	}
	kind, ok := identityKindForPolicySubject(subjectType)
	if !ok {
		return identityURLMatch{}, false
	}
	id, suffix, ok := firstPathSegment(strings.TrimPrefix(rest, "/"))
	if !ok || (suffix != "/capabilities" && !strings.HasPrefix(suffix, "/capabilities/")) {
		return identityURLMatch{}, false
	}
	return identityURLMatch{
		kind: kind,
		id:   id,
		replace: func(newID string) string {
			return prefix + subjectType + "/" + newID + suffix
		},
	}, true
}

func identityKindForPolicySubject(subjectType string) (identity.Kind, bool) {
	switch subjectType {
	case subjectTypeGroups:
		return identity.KindGroup, true
	case subjectTypeRoles:
		return identity.KindRole, true
	default:
		// Agents are excluded: prism_ids are already opaque (UUID4, not ULID)
		// per spec §5 — "/agents/{prism_id} - UNCHANGED". Routing them through
		// the name-resolver would 404 every legitimate agent request.
		return "", false
	}
}

func firstPathSegment(rest string) (segment, suffix string, ok bool) {
	if rest == "" || strings.HasPrefix(rest, "/") {
		return "", "", false
	}
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		if i == 0 {
			return "", "", false
		}
		return rest[:i], rest[i:], true
	}
	return rest, "", true
}

func identityCompatLocation(r *http.Request, newPath string) string {
	escapedNewPath := (&url.URL{Path: newPath}).EscapedPath()
	locationPath := escapedNewPath
	if originalPath := requestURIPath(r); originalPath != "" {
		if oldPath := r.URL.EscapedPath(); oldPath != "" && strings.HasSuffix(originalPath, oldPath) {
			locationPath = strings.TrimSuffix(originalPath, oldPath) + escapedNewPath
		}
	}
	if r.URL.RawQuery == "" {
		return locationPath
	}
	return locationPath + "?" + r.URL.RawQuery
}

func requestURIPath(r *http.Request) string {
	if r.RequestURI == "" {
		return ""
	}
	u, err := url.ParseRequestURI(r.RequestURI)
	if err == nil {
		return u.EscapedPath()
	}
	path := r.RequestURI
	if i := strings.IndexByte(path, '?'); i >= 0 {
		path = path[:i]
	}
	return path
}
