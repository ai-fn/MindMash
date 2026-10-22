package events

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mindmesh/mindmesh/internal/platform/testhelpers"
)

func TestLoadConfig_Defaults(t *testing.T) {
	testhelpers.UnsetAll(t,
		"KAFKA_BROKERS",
		"KAFKA_GROUP_ID",
		"KAFKA_CLIENT_ID",
		"KAFKA_READ_TIMEOUT",
		"KAFKA_WRITE_TIMEOUT",
		"KAFKA_MAX_RETRIES",
	)

	cfg, err := LoadConfig()
	require.NoError(t, err)

	assert.Equal(t, []string{"localhost:9092"}, cfg.Brokers)
	assert.Equal(t, "mindmesh", cfg.GroupID)
	assert.Equal(t, "mindmesh-events", cfg.ClientID)
	assert.Equal(t, 10*time.Second, cfg.ReadTimeout)
	assert.Equal(t, 10*time.Second, cfg.WriteTimeout)
	assert.Equal(t, 3, cfg.MaxRetries)
}

func TestLoadConfig_BrokerSplit(t *testing.T) {
	t.Setenv("KAFKA_BROKERS", "broker1:9092,broker2:9092")

	cfg, err := LoadConfig()
	require.NoError(t, err)

	assert.Equal(t, []string{"broker1:9092", "broker2:9092"}, cfg.Brokers)
}

func TestLoadConfig_EmptyBrokers(t *testing.T) {
	t.Setenv("KAFKA_BROKERS", "")

	_, err := LoadConfig()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "KAFKA_BROKERS")
}

func TestLoadConfig_InvalidEnvVars(t *testing.T) {
	testhelpers.UnsetAll(t,
		"KAFKA_BROKERS",
		"KAFKA_GROUP_ID",
		"KAFKA_CLIENT_ID",
		"KAFKA_READ_TIMEOUT",
		"KAFKA_WRITE_TIMEOUT",
		"KAFKA_MAX_RETRIES",
	)

	tests := []struct {
		name    string
		envKey  string
		envVal  string
		wantErr string
	}{
		{
			name:    "KAFKA_READ_TIMEOUT bad value",
			envKey:  "KAFKA_READ_TIMEOUT",
			envVal:  "bad",
			wantErr: "KAFKA_READ_TIMEOUT",
		},
		{
			name:    "KAFKA_WRITE_TIMEOUT bad value",
			envKey:  "KAFKA_WRITE_TIMEOUT",
			envVal:  "bad",
			wantErr: "KAFKA_WRITE_TIMEOUT",
		},
		{
			name:    "KAFKA_MAX_RETRIES not an int",
			envKey:  "KAFKA_MAX_RETRIES",
			envVal:  "notanint",
			wantErr: "KAFKA_MAX_RETRIES",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(tc.envKey, tc.envVal)
			_, err := LoadConfig()
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

func TestNewRelayCapsBatch(t *testing.T) {
	r := NewRelay(nil, nil, nil, RelayConfig{Batch: 10_000})
	assert.Equal(t, maxRelayBatch, r.batch)
}
