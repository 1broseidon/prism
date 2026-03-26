package authserver

import (
	"embed"
	"html/template"
	"net/http"
)

//go:embed consent.html
var consentHTML embed.FS

var consentTmpl = template.Must(template.ParseFS(consentHTML, "consent.html"))

type consentData struct {
	ClientName      string
	ClientID        string
	ResponseType    string
	RedirectURI     string
	State           string
	CodeChallenge   string
	ChallengeMethod string
	Scopes          string
}

func (s *Server) renderConsent(w http.ResponseWriter, data *consentData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := consentTmpl.Execute(w, data); err != nil {
		s.logger.Error("failed to render consent page", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}
