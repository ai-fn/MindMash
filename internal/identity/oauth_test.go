package identity

import (
	"encoding/json"
	"testing"
)

func TestClaimBool(t *testing.T) {
	tests := []struct {
		json    string
		want    bool
		wantErr bool
	}{
		{`{"email_verified":true}`, true, false},
		{`{"email_verified":"true"}`, true, false}, // Apple
		{`{"email_verified":false}`, false, false},
		{`{"email_verified":"false"}`, false, false},
		{`{"email_verified":null}`, false, false},
		{`{}`, false, false},
		{`{"email_verified":"yes"}`, false, true},
	}
	for _, tc := range tests {
		var claims struct {
			EmailVerified claimBool `json:"email_verified"`
		}
		err := json.Unmarshal([]byte(tc.json), &claims)
		if (err != nil) != tc.wantErr || bool(claims.EmailVerified) != tc.want {
			t.Fatalf("%s: want %v (err %v), got %v (%v)", tc.json, tc.want, tc.wantErr, claims.EmailVerified, err)
		}
	}
}
