package caddyhttp

import (
	"context"
	"testing"
	"time"

	"golang.org/x/net/http2"
)

func TestProxySendCreditStallOptInAndAuthenticatedContext(t *testing.T) {
	for _, tc := range []struct {
		name, policy, evidence string
		enabled                bool
	}{
		{"default_off", "", "1", false},
		{"explicit_off", "0", "1", false},
		{"non_exact_opt_in", "true", "1", false},
		{"no_metadata_handle", "1", "", false},
		{"enabled", "1", "1", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("PCS_PROXY_SEND_CREDIT_STALL", tc.policy)
			t.Setenv("PCS_PROXY_EVIDENCE", tc.evidence)
			s := &http2.Server{}
			configureProxySendCreditStall(s)
			if !tc.enabled {
				if s.SendCreditStallTimeout != 0 || s.SendCreditStallEnabled != nil {
					t.Fatal("policy enabled without both explicit switches")
				}
				return
			}
			if s.SendCreditStallTimeout != 10*time.Second || s.MaxConcurrentSendCreditStallDrains != 16 || s.SendCreditStallEnabled == nil {
				t.Fatal("bounded policy configuration missing")
			}
			if s.SendCreditStallEnabled(context.Background()) {
				t.Fatal("ordinary uninstrumented context enabled the policy")
			}
			p := &ProxyEvidence{}
			ctx := context.WithValue(context.Background(), proxyEvidenceKey{}, p)
			if s.SendCreditStallEnabled(ctx) {
				t.Fatal("unauthenticated connection metadata enabled the policy")
			}
			// The authentication handler owns this transition. Do not call its
			// logging/collector startup from this isolated selector test.
			p.active.Store(true)
			if !s.SendCreditStallEnabled(ctx) {
				t.Fatal("authenticated proxy metadata did not enable the policy")
			}
		})
	}
}
