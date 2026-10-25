package syncer

import (
	"testing"
	"time"

	"github.com/mindmesh/mindmesh/internal/platform/testhelpers"
)

func TestAppliedOpsPurgeLastSuccess_StartsAtProcessStart(t *testing.T) {
	v, ok := testhelpers.MetricValue(t, "mindmesh_sync_applied_ops_purge_last_success_timestamp_seconds", nil)
	if !ok || time.Since(time.Unix(int64(v), 0)) > time.Hour {
		t.Fatalf("gauge = %v (present %v); a 0 start would page the stale-purge alert before the first purge", v, ok)
	}
}
