package adminauth

import (
	"strings"

	"github.com/1broseidon/prism/internal/config"
)

// resolveRole returns the role the given user is granted by the first matching
// rule, or "" if no rule matches.
//
// Match logic:
//   - email: case-insensitive exact match against rule.Emails
//   - domain: case-insensitive match of the email's domain against rule.Domains
//   - group: case-sensitive intersection of user's groups with rule.Groups
//
// A rule matches if ANY of its matchers contains a value from the user.
func resolveRole(email string, groups []string, rules []config.AdminAuthRule) Role {
	emailLower := strings.ToLower(email)
	domain := ""
	if i := strings.IndexByte(emailLower, '@'); i >= 0 {
		domain = emailLower[i+1:]
	}
	groupSet := make(map[string]struct{}, len(groups))
	for _, g := range groups {
		groupSet[g] = struct{}{}
	}

	for _, r := range rules {
		if matchEmails(r.Emails, emailLower) ||
			matchDomains(r.Domains, domain) ||
			matchGroups(r.Groups, groupSet) {
			return Role(r.Role)
		}
	}
	return ""
}

func matchEmails(list []string, target string) bool {
	if target == "" {
		return false
	}
	for _, e := range list {
		if strings.EqualFold(e, target) {
			return true
		}
	}
	return false
}

func matchDomains(list []string, target string) bool {
	if target == "" {
		return false
	}
	for _, d := range list {
		if strings.EqualFold(d, target) {
			return true
		}
	}
	return false
}

func matchGroups(list []string, userGroups map[string]struct{}) bool {
	for _, g := range list {
		if _, ok := userGroups[g]; ok {
			return true
		}
	}
	return false
}
