package validate_test

import (
	"strings"
	"testing"

	"github.com/mindmesh/mindmesh/internal/platform/apperr"
	"github.com/mindmesh/mindmesh/internal/platform/validate"
)

const goodID = "0190f3a8-7b2c-7d4e-9f00-123456789abc"

func TestIsUUID(t *testing.T) {
	tests := []struct {
		in   string
		want bool
	}{
		{goodID, true},
		{"", false},
		{"not-a-uuid", false},
		{strings.ToUpper(goodID), false},
		{"{" + goodID + "}", false},
		{"urn:uuid:" + goodID, false},
		{strings.ReplaceAll(goodID, "-", ""), false},
	}
	for _, tt := range tests {
		if got := validate.IsUUID(tt.in); got != tt.want {
			t.Errorf("IsUUID(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestChecks(t *testing.T) {
	many := make([]string, validate.MaxBulkIDs+1)
	for i := range many {
		many[i] = goodID
	}
	tests := []struct {
		name string
		err  error
		ok   bool
	}{
		{"id ok", validate.ID("id", goodID), true},
		{"id empty", validate.ID("id", ""), false},
		{"id bad", validate.ID("id", "x"), false},
		{"optional empty", validate.OptionalID("parent_id", ""), true},
		{"optional bad", validate.OptionalID("parent_id", "x"), false},
		{"ids ok", validate.IDs("ids", []string{goodID, goodID}), true},
		{"ids empty list", validate.IDs("ids", nil), true},
		{"ids bad element", validate.IDs("ids", []string{goodID, "x"}), false},
		{"ids over cap", validate.IDs("ids", many), false},
		{"ids at cap", validate.IDs("ids", many[1:]), true},
		{"name ok", validate.Name("name", "Spanish::Verbs", 20), true},
		{"name multibyte at cap", validate.Name("name", "ёжик", 4), true},
		{"name over cap", validate.Name("name", "ёжики", 4), false},
		{"name blank", validate.Name("name", "  \t", 10), false},
		{"name control", validate.Name("name", "a\nb", 10), false},
		{"name bad utf8", validate.Name("name", "a\xffb", 10), false},
		{"text ok", validate.Text("css", ".card{}", 10), true},
		{"text empty", validate.Text("css", "", 10), true},
		{"text newline", validate.Text("css", "a\nb", 10), true},
		{"text over cap", validate.Text("css", strings.Repeat("a", 11), 10), false},
		{"text nul", validate.Text("css", "a\x00b", 10), false},
		{"text bad utf8", validate.Text("css", "a\xffb", 10), false},
		{"count ok", validate.Count("tags", 3, 3), true},
		{"count over", validate.Count("tags", 4, 3), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.ok {
				if tt.err != nil {
					t.Fatalf("unexpected error: %v", tt.err)
				}
				return
			}
			if !apperr.IsInvalidArgument(tt.err) {
				t.Fatalf("want InvalidArgument, got %v", tt.err)
			}
		})
	}
}
