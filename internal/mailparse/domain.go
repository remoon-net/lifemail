package mailparse

import "strings"

// Domain returns the lowercased domain part of an address, or "" when the
// address has no domain.
func Domain(address string) string {
	at := strings.LastIndex(address, "@")
	if at < 0 || at == len(address)-1 {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(address[at+1:]))
}
