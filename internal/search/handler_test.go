package search

import (
	"context"
	"strings"
	"testing"

	searchv1 "github.com/mindmesh/mindmesh/api/search/v1"
	"github.com/mindmesh/mindmesh/internal/platform/auth"
	platformerrors "github.com/mindmesh/mindmesh/internal/platform/errors"
	"github.com/mindmesh/mindmesh/internal/platform/validate"
)

func TestSearch_RejectsOversizedQuery(t *testing.T) {
	ctx := auth.WithIdentity(context.Background(), auth.IdentityContext{UserID: "u1"})
	q := strings.Repeat("a ", validate.MaxQueryBytes)
	_, err := NewHandler(nil).Search(ctx, &searchv1.SearchRequest{Q: q})
	if !platformerrors.IsInvalidArgument(err) {
		t.Fatalf("want InvalidArgument, got %v", err)
	}
}
