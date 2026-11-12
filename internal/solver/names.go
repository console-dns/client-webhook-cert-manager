package solver

import (
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/util/validation"
)

// recordLocation uses the target already resolved by cert-manager, including
// any configured CNAME delegation. Console DNS accepts relative record names.
func recordLocation(fqdn, resolvedZone string) (zone, name string, err error) {
	zone = strings.ToLower(strings.TrimSuffix(resolvedZone, "."))
	fqdn = strings.ToLower(strings.TrimSuffix(fqdn, "."))
	if zone == "" || !strings.Contains(zone, ".") || len(validation.IsDNS1123Subdomain(zone)) != 0 {
		return "", "", fmt.Errorf("resolvedZone must be a DNS zone with at least two labels")
	}
	if fqdn == zone {
		return "", "", fmt.Errorf("Console DNS does not support a challenge at the zone apex")
	}
	if !strings.HasSuffix(fqdn, "."+zone) {
		return "", "", fmt.Errorf("resolvedFQDN must be within resolvedZone")
	}
	if len(fqdn) > 253 {
		return "", "", fmt.Errorf("resolvedFQDN exceeds the DNS name length limit")
	}
	name = strings.TrimSuffix(fqdn, "."+zone)
	for _, label := range strings.Split(name, ".") {
		if len(label) == 0 || len(label) > 63 {
			return "", "", fmt.Errorf("resolvedFQDN contains an invalid DNS label length")
		}
		for _, char := range label {
			if (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '_' && char != '-' {
				return "", "", fmt.Errorf("resolvedFQDN contains an unsupported DNS label character")
			}
		}
		if label[0] == '-' || label[len(label)-1] == '-' {
			return "", "", fmt.Errorf("resolvedFQDN contains a DNS label with a leading or trailing hyphen")
		}
	}
	return zone, name, nil
}
