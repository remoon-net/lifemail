package eas

import (
	"encoding/xml"
	"io"
	"net/http"
	"strings"
)

// Autodiscover serves the minimal mobile sync autodiscover response so that
// clients only need an email address and password.
func Autodiscover() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			http.Error(w, "autodiscover requires POST", http.StatusMethodNotAllowed)
			return
		}
		body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		email := extractEmailAddress(string(body))

		scheme := "https"
		if proto := r.Header.Get("X-Forwarded-Proto"); proto != "" {
			scheme = proto
		}
		host := r.Host
		if host == "" {
			host = "localhost"
		}
		serverURL := scheme + "://" + host + "/Microsoft-Server-ActiveSync"

		type server struct {
			Type string `xml:"Type"`
			URL  string `xml:"Url"`
			Name string `xml:"Name"`
		}
		type settings struct {
			Server server `xml:"Server"`
		}
		type action struct {
			Settings settings `xml:"Settings"`
		}
		type user struct {
			DisplayName  string `xml:"DisplayName"`
			EMailAddress string `xml:"EMailAddress"`
		}
		type response struct {
			XMLName xml.Name `xml:"Response"`
			Culture string   `xml:"Culture"`
			User    user     `xml:"User"`
			Action  action   `xml:"Action"`
		}
		type autodiscover struct {
			XMLName  xml.Name `xml:"Autodiscover"`
			Xmlns    string   `xml:"xmlns,attr"`
			Response response `xml:"Response"`
		}

		out := autodiscover{
			Xmlns: "http://schemas.microsoft.com/exchange/autodiscover/responseschema/2006",
			Response: response{
				Culture: "en:en",
				User:    user{DisplayName: email, EMailAddress: email},
				Action: action{Settings: settings{Server: server{
					Type: "MobileSync",
					URL:  serverURL,
					Name: serverURL,
				}}},
			},
		}
		w.Header().Set("Content-Type", "text/xml; charset=utf-8")
		_ = xml.NewEncoder(w).Encode(out)
	})
}

func extractEmailAddress(body string) string {
	const open = "<EMailAddress>"
	const closeTag = "</EMailAddress>"
	if i := strings.Index(strings.ToLower(body), strings.ToLower(open)); i >= 0 {
		rest := body[i+len(open):]
		if j := strings.Index(strings.ToLower(rest), strings.ToLower(closeTag)); j >= 0 {
			return strings.TrimSpace(rest[:j])
		}
	}
	return ""
}
