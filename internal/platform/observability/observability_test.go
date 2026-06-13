package observability

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mindmesh/mindmesh/internal/platform/testhelpers"
)

func TestLoadConfig_Defaults(t *testing.T) {
	testhelpers.UnsetAll(t,
		"OTEL_SERVICE_NAME",
		"OTEL_EXPORTER_OTLP_ENDPOINT",
		"OTEL_SAMPLING_RATE",
		"METRICS_ADDR",
	)

	cfg, err := LoadConfig()
	require.NoError(t, err)

	assert.Equal(t, "mindmesh", cfg.ServiceName)
	assert.Equal(t, "http://localhost:4317", cfg.OTLPEndpoint)
	assert.Equal(t, 1.0, cfg.SamplingRate)
	assert.Equal(t, ":9090", cfg.MetricsAddr)
}

func TestLoadConfig_InvalidSamplingRate(t *testing.T) {
	tests := []struct {
		name  string
		value string
	}{
		{"below zero", "-0.1"},
		{"above one", "1.1"},
		{"far below", "-100"},
		{"far above", "2.5"},
		{"not a number", "bad"},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("OTEL_SAMPLING_RATE", tc.value)
			_, err := LoadConfig()
			require.Error(t, err)
			assert.Contains(t, err.Error(), "OTEL_SAMPLING_RATE")
		})
	}
}

func TestLoadConfig_EnvOverride(t *testing.T) {
	t.Setenv("OTEL_SERVICE_NAME", "my-service")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://otel-collector:4317")
	t.Setenv("OTEL_SAMPLING_RATE", "0.5")
	t.Setenv("METRICS_ADDR", ":9100")

	cfg, err := LoadConfig()
	require.NoError(t, err)

	assert.Equal(t, "my-service", cfg.ServiceName)
	assert.Equal(t, "http://otel-collector:4317", cfg.OTLPEndpoint)
	assert.Equal(t, 0.5, cfg.SamplingRate)
	assert.Equal(t, ":9100", cfg.MetricsAddr)
}
