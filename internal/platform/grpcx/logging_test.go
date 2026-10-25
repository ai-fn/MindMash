package grpcx

import (
	"context"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/mindmesh/mindmesh/internal/platform/log"
)

func TestObserve_LogLevels(t *testing.T) {
	const health, rpc = "/grpc.health.v1.Health/Check", "/collection.v1.NoteService/GetNote"
	tests := []struct {
		name   string
		method string
		err    error
		want   zapcore.Level
	}{
		// Probes poll every few seconds per pod, so their success lines would drown the request log.
		{"health ok", health, nil, zapcore.DebugLevel},
		{"health failing", health, status.Error(codes.Unavailable, "x"), zapcore.WarnLevel},
		{"ok", rpc, nil, zapcore.InfoLevel},
		{"not found", rpc, status.Error(codes.NotFound, "x"), zapcore.InfoLevel},
		{"internal", rpc, status.Error(codes.Internal, "x"), zapcore.WarnLevel},
		{"unavailable", rpc, status.Error(codes.Unavailable, "x"), zapcore.WarnLevel},
		{"deadline", rpc, status.Error(codes.DeadlineExceeded, "x"), zapcore.WarnLevel},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			core, logs := observer.New(zapcore.DebugLevel)
			t.Cleanup(log.Replace(zap.New(core)))

			observe(context.Background(), tc.method, time.Now(), tc.err)

			entries := logs.All()
			if len(entries) != 1 || entries[0].Level != tc.want {
				t.Fatalf("entries = %v, want one at %v", entries, tc.want)
			}
		})
	}
}
