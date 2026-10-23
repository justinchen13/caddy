package caddyhttp

import (
	"os"
	"time"

	"golang.org/x/net/http2"
)

// This experimental policy needs the existing connection metadata handle.
// EnableProxyEvidence marks the outer connection after a diagnostic CONNECT
// passes the configured forward-proxy authentication policy. That mark persists
// for the connection; it does not authenticate every later stream or prove an
// origin dial succeeded. Never-marked and uninstrumented connections stay out.
// Disabling evidence also disables this experimental integration.
func configureProxySendCreditStall(s *http2.Server) {
	if os.Getenv("PCS_PROXY_SEND_CREDIT_STALL") != "1" || os.Getenv("PCS_PROXY_EVIDENCE") != "1" {
		return
	}
	s.SendCreditStallTimeout = 10 * time.Second
	s.MaxConcurrentSendCreditStallDrains = 16
	s.SendCreditStallEnabled = proxyH2ObservationEnabled
}
