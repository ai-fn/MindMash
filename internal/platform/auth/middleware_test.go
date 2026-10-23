package auth

import (
	"context"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func TestGRPCAuthInterceptorPublicMethods(t *testing.T) {
	interceptor := GRPCAuthInterceptor(newEphemeralSigner(t, defaultCfg()), "/app.v1.Auth/Login")
	handler := func(context.Context, any) (any, error) { return "ok", nil }

	tests := []struct {
		method     string
		wantPublic bool
	}{
		{"/grpc.health.v1.Health/Check", true},
		{"/grpc.health.v1.Health/List", true},
		{"/app.v1.Auth/Login", true},
		{"/app.v1.Auth/LoginX", false},
		{"/app.v1.Auth/GetMe", false},
		{"/evil.grpc.health.v1.Health/Check", false},
		{"/app.v1.grpc.reflection/Steal", false},
		{"/grpc.health.v1.HealthX/Check", false},
	}
	for _, tc := range tests {
		t.Run(tc.method, func(t *testing.T) {
			_, err := interceptor(context.Background(), nil, &grpc.UnaryServerInfo{FullMethod: tc.method}, handler)
			if tc.wantPublic && err != nil {
				t.Fatalf("public method rejected: %v", err)
			}
			if !tc.wantPublic && status.Code(err) != codes.Unauthenticated {
				t.Fatalf("got %v, want Unauthenticated", status.Code(err))
			}
		})
	}
}

type fakeServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s fakeServerStream) Context() context.Context { return s.ctx }

func TestGRPCAuthStreamInterceptor(t *testing.T) {
	s := newEphemeralSigner(t, defaultCfg())
	interceptor := GRPCAuthStreamInterceptor(s)
	access, _, err := s.SignAccess("u1", "p1", "en", []string{"user"})
	if err != nil {
		t.Fatal(err)
	}
	refresh, _, err := s.SignRefresh("u1", "p1")
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name     string
		method   string
		authz    string
		wantCode codes.Code
		wantUser string
	}{
		{"access token", "/app.v1.Feed/Watch", "Bearer " + access, codes.OK, "u1"},
		{"no token", "/app.v1.Feed/Watch", "", codes.Unauthenticated, ""},
		{"refresh token", "/app.v1.Feed/Watch", "Bearer " + refresh, codes.Unauthenticated, ""},
		{"health watch", "/grpc.health.v1.Health/Watch", "", codes.OK, ""},
		{"reflection", "/grpc.reflection.v1.ServerReflection/ServerReflectionInfo", "", codes.OK, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			md := metadata.New(nil)
			if tc.authz != "" {
				md.Set("authorization", tc.authz)
			}
			ss := fakeServerStream{ctx: metadata.NewIncomingContext(context.Background(), md)}
			var gotUser string
			err := interceptor(nil, ss, &grpc.StreamServerInfo{FullMethod: tc.method}, func(_ any, stream grpc.ServerStream) error {
				if id, ok := IdentityFromContext(stream.Context()); ok {
					gotUser = id.UserID
				}
				return nil
			})
			if status.Code(err) != tc.wantCode {
				t.Fatalf("code: got %v, want %v", status.Code(err), tc.wantCode)
			}
			if gotUser != tc.wantUser {
				t.Fatalf("user: got %q, want %q", gotUser, tc.wantUser)
			}
		})
	}
}
