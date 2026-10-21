package eas

import (
	"strings"
	"testing"

	"remoon.net/lifemail/internal/wbxml"
)

// MIUI Mail sends this shape when the user asks to search the server:
//
//	Store > Name = Mailbox
//	Store > Query > And > Class=Email, FreeText="test",
//	                     LessThan > DateReceived > Value=<date>
//	Store > Options > RebuildResults, DeepTraversal, Range=0-9
//
// The date inside LessThan must not become part of the search phrase.
func TestSearchParamsIgnoresDateConditions(t *testing.T) {
	query := wbxml.N(pageSearch, "Query",
		wbxml.N(pageSearch, "And",
			wbxml.T(pageSearch, "Class", "Email"),
			wbxml.T(pageSearch, "FreeText", "test"),
			wbxml.N(pageSearch, "LessThan",
				wbxml.N(pageSearch, "DateReceived"),
				wbxml.T(pageSearch, "Value", "2026-10-21T00:00:00.000Z"))))
	store := wbxml.N(pageSearch, "Store",
		wbxml.T(pageSearch, "Name", "Mailbox"),
		query,
		wbxml.N(pageSearch, "Options",
			wbxml.N(pageSearch, "RebuildResults"),
			wbxml.N(pageSearch, "DeepTraversal"),
			wbxml.T(pageSearch, "Range", "0-9")))

	term, start, limit := searchParams(store)
	if term != "test" {
		t.Fatalf("term = %q, want %q", term, "test")
	}
	if start != 0 || limit != 10 {
		t.Fatalf("range = %d..%d, want 0..10", start, limit)
	}
	if strings.Contains(term, "2026") {
		t.Fatalf("date leaked into the search term: %q", term)
	}
}

// An equality test still contributes its value as a keyword.
func TestSearchTermsIncludesEqualToValue(t *testing.T) {
	query := wbxml.N(pageSearch, "Query",
		wbxml.N(pageSearch, "EqualTo",
			wbxml.N(pageSearch, "Subject"),
			wbxml.T(pageSearch, "Value", "invoice")))
	term, _, _ := searchParams(wbxml.N(pageSearch, "Store", query))
	if term != "invoice" {
		t.Fatalf("term = %q, want invoice", term)
	}
}
