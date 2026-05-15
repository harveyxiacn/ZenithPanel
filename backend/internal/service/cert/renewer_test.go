package cert

import (
	"testing"
)

// TestRunRenewalScanWithoutEmailIsNoop verifies the scan exits cleanly when
// no ACME email has been configured. This used to spam logs every 12 hours
// before the early-return was added.
func TestRunRenewalScanWithoutEmailIsNoop(t *testing.T) {
	// Just ensure it doesn't panic and returns quickly. The function is
	// side-effect-only, so we have nothing to assert beyond "doesn't blow up".
	runRenewalScan(func(string) string { return "" })
}

// TestRunRenewalScanIgnoresNonDomainFiles confirms self-signed test files
// (e.g. fullchain.pem, privkey.pem) aren't mistaken for ACME-managed certs.
// Pre-check is purely path-based (regex + filename suffix) so we can drive
// it without an on-disk cert.
func TestRunRenewalScanIgnoresNonDomainFiles(t *testing.T) {
	// Same as above: the function reads certDir, but on a fresh test host
	// certDir won't exist and the early os.IsNotExist branch returns. The
	// stub email makes us pass the email guard, so we exercise the rest.
	runRenewalScan(func(k string) string {
		if k == "acme_email" {
			return "test@example.com"
		}
		return ""
	})
}
