package config

import (
	"bufio"
	"os"
	"strings"
)

// defaultResolvConfPath is the standard location of resolv.conf.
// Overridable via resolvConfPath so tests can point at a fixture file.
const defaultResolvConfPath = "/etc/resolv.conf"

// resolvConfPath is the file consulted by discoverFromResolvConf. Tests
// reassign it; production callers leave it alone.
var resolvConfPath = defaultResolvConfPath

// discoverFromResolvConf parses /etc/resolv.conf and returns site,
// tenant, and region values inferred from search-line entries set up by
// the F5 XC CE platform. Returns "" for any field that can't be derived;
// the caller should treat that as "leave any explicit env value alone."
//
// XC search-line pattern:
//
//	search <ns>.svc.<site>.<tenant>.tenant.local
//	       svc.<site>.<tenant>.tenant.local
//	       <site>.<tenant>.tenant.local
//	       <region>.compute.internal
//
// A token with the shape `<site>.<tenant>.tenant.local` (exactly four
// dot-separated labels) yields site + tenant. A token of shape
// `<region>.compute.internal` (exactly three labels) yields region.
// First match per field wins; later entries don't overwrite.
func discoverFromResolvConf() (site, tenant, region string) {
	f, err := os.Open(resolvConfPath)
	if err != nil {
		return "", "", ""
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "search") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		for _, tok := range fields[1:] {
			if s, t, ok := parseTenantLocalToken(tok); ok {
				if site == "" {
					site = s
				}
				if tenant == "" {
					tenant = t
				}
			} else if r, ok := parseComputeInternalToken(tok); ok {
				if region == "" {
					region = r
				}
			}
		}
	}
	return site, tenant, region
}

func parseTenantLocalToken(tok string) (site, tenant string, ok bool) {
	if !strings.HasSuffix(tok, ".tenant.local") {
		return "", "", false
	}
	parts := strings.Split(tok, ".")
	if len(parts) != 4 {
		return "", "", false
	}
	if parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func parseComputeInternalToken(tok string) (region string, ok bool) {
	if !strings.HasSuffix(tok, ".compute.internal") {
		return "", false
	}
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		return "", false
	}
	if parts[0] == "" {
		return "", false
	}
	return parts[0], true
}
