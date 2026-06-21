package sync

import (
	"context"

	collectionv1 "github.com/mindmesh/mindmesh/api/collection/v1"
	"google.golang.org/grpc"
)

// CollectionClient is the subset of Collection's public mutation RPCs Apply
// needs. A thin interface (not the raw generated clients) so tests can fake
// it without a real gRPC server.
type CollectionClient interface {
	GetNoteType(ctx context.Context, req *collectionv1.GetNoteTypeRequest) (*collectionv1.GetNoteTypeResponse, error)
	UpdateNoteType(ctx context.Context, req *collectionv1.UpdateNoteTypeRequest) (*collectionv1.UpdateNoteTypeResponse, error)
	DeleteNoteType(ctx context.Context, req *collectionv1.DeleteNoteTypeRequest) (*collectionv1.DeleteNoteTypeResponse, error)

	GetNote(ctx context.Context, req *collectionv1.GetNoteRequest) (*collectionv1.GetNoteResponse, error)
	UpdateNote(ctx context.Context, req *collectionv1.UpdateNoteRequest) (*collectionv1.UpdateNoteResponse, error)
	DeleteNote(ctx context.Context, req *collectionv1.DeleteNoteRequest) (*collectionv1.DeleteNoteResponse, error)

	GetDeck(ctx context.Context, req *collectionv1.GetDeckRequest) (*collectionv1.GetDeckResponse, error)
	UpdateDeck(ctx context.Context, req *collectionv1.UpdateDeckRequest) (*collectionv1.UpdateDeckResponse, error)
	DeleteDeck(ctx context.Context, req *collectionv1.DeleteDeckRequest) (*collectionv1.DeleteDeckResponse, error)

	GetDeckConfig(ctx context.Context, req *collectionv1.GetDeckConfigRequest) (*collectionv1.GetDeckConfigResponse, error)
	UpdateDeckConfig(ctx context.Context, req *collectionv1.UpdateDeckConfigRequest) (*collectionv1.UpdateDeckConfigResponse, error)
	DeleteDeckConfig(ctx context.Context, req *collectionv1.DeleteDeckConfigRequest) (*collectionv1.DeleteDeckConfigResponse, error)

	DeleteFilteredDeck(ctx context.Context, req *collectionv1.DeleteFilteredDeckRequest) (*collectionv1.DeleteFilteredDeckResponse, error)
}

// grpcCollectionClient adapts the generated per-service Collection gRPC
// clients to CollectionClient. DeckConfig RPCs route through `decks` because
// DeckConfig is a sub-resource of DeckService, not its own gRPC service (see
// api/collection/v1/collection.proto's DeckService definition).
type grpcCollectionClient struct {
	noteTypes collectionv1.NoteTypeServiceClient
	notes     collectionv1.NoteServiceClient
	decks     collectionv1.DeckServiceClient // also serves DeckConfig RPCs (same service)
	filtered  collectionv1.FilteredDeckServiceClient
}

// NewGRPCCollectionClient adapts the generated per-service gRPC clients to CollectionClient.
func NewGRPCCollectionClient(conn grpc.ClientConnInterface) CollectionClient {
	return &grpcCollectionClient{
		noteTypes: collectionv1.NewNoteTypeServiceClient(conn),
		notes:     collectionv1.NewNoteServiceClient(conn),
		decks:     collectionv1.NewDeckServiceClient(conn),
		filtered:  collectionv1.NewFilteredDeckServiceClient(conn),
	}
}

func (c *grpcCollectionClient) GetNoteType(ctx context.Context, req *collectionv1.GetNoteTypeRequest) (*collectionv1.GetNoteTypeResponse, error) {
	return c.noteTypes.GetNoteType(ctx, req)
}

func (c *grpcCollectionClient) UpdateNoteType(ctx context.Context, req *collectionv1.UpdateNoteTypeRequest) (*collectionv1.UpdateNoteTypeResponse, error) {
	return c.noteTypes.UpdateNoteType(ctx, req)
}

func (c *grpcCollectionClient) DeleteNoteType(ctx context.Context, req *collectionv1.DeleteNoteTypeRequest) (*collectionv1.DeleteNoteTypeResponse, error) {
	return c.noteTypes.DeleteNoteType(ctx, req)
}

func (c *grpcCollectionClient) GetNote(ctx context.Context, req *collectionv1.GetNoteRequest) (*collectionv1.GetNoteResponse, error) {
	return c.notes.GetNote(ctx, req)
}

func (c *grpcCollectionClient) UpdateNote(ctx context.Context, req *collectionv1.UpdateNoteRequest) (*collectionv1.UpdateNoteResponse, error) {
	return c.notes.UpdateNote(ctx, req)
}

func (c *grpcCollectionClient) DeleteNote(ctx context.Context, req *collectionv1.DeleteNoteRequest) (*collectionv1.DeleteNoteResponse, error) {
	return c.notes.DeleteNote(ctx, req)
}

func (c *grpcCollectionClient) GetDeck(ctx context.Context, req *collectionv1.GetDeckRequest) (*collectionv1.GetDeckResponse, error) {
	return c.decks.GetDeck(ctx, req)
}

func (c *grpcCollectionClient) UpdateDeck(ctx context.Context, req *collectionv1.UpdateDeckRequest) (*collectionv1.UpdateDeckResponse, error) {
	return c.decks.UpdateDeck(ctx, req)
}

func (c *grpcCollectionClient) DeleteDeck(ctx context.Context, req *collectionv1.DeleteDeckRequest) (*collectionv1.DeleteDeckResponse, error) {
	return c.decks.DeleteDeck(ctx, req)
}

func (c *grpcCollectionClient) GetDeckConfig(ctx context.Context, req *collectionv1.GetDeckConfigRequest) (*collectionv1.GetDeckConfigResponse, error) {
	return c.decks.GetDeckConfig(ctx, req)
}

func (c *grpcCollectionClient) UpdateDeckConfig(ctx context.Context, req *collectionv1.UpdateDeckConfigRequest) (*collectionv1.UpdateDeckConfigResponse, error) {
	return c.decks.UpdateDeckConfig(ctx, req)
}

func (c *grpcCollectionClient) DeleteDeckConfig(ctx context.Context, req *collectionv1.DeleteDeckConfigRequest) (*collectionv1.DeleteDeckConfigResponse, error) {
	return c.decks.DeleteDeckConfig(ctx, req)
}

func (c *grpcCollectionClient) DeleteFilteredDeck(ctx context.Context, req *collectionv1.DeleteFilteredDeckRequest) (*collectionv1.DeleteFilteredDeckResponse, error) {
	return c.filtered.DeleteFilteredDeck(ctx, req)
}
