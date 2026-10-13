package openstackprovider

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
)

// Customer support reported deletion failures at 50 or more characters.
// Stay below that boundary; the public NSM guide does not specify a name limit.
const ktCloudTierNameLimit = 49

// Hash the full identity rather than a truncated display prefix. Generation is
// included even though it is no longer written out in the provider name.
func ktCloudTierBaseName(labID string, generation int64) string {
	digest := sha256.Sum256([]byte(strings.TrimSpace(labID) + ":" + strconv.FormatInt(generation, 10)))
	return "labbit-" + hex.EncodeToString(digest[:10])
}

func ktCloudLabTierName(labID string, generation int64) string {
	return ktCloudTierBaseName(labID, generation) + "-lab"
}

func ktCloudManagementTierName(labID string, generation int64, vmKey string) string {
	digest := sha256.Sum256([]byte(vmKey))
	return ktCloudTierBaseName(labID, generation) + "-m-" + hex.EncodeToString(digest[:8])
}

func ktCloudManagementTierNameMatches(name, labID string, generation int64, vmKey string) bool {
	return name == ktCloudManagementTierName(labID, generation, vmKey) ||
		name == provisionBaseName(labID, generation)+"-"+safeName(vmKey, 28)+"-management-tier"
}

func ktCloudTierCandidate(name, labID string, generation int64, vmKeys []string) (string, bool) {
	if name == ktCloudLabTierName(labID, generation) || name == provisionBaseName(labID, generation)+"-lab-tier" {
		return "lab-tier", true
	}
	for _, key := range vmKeys {
		if ktCloudManagementTierNameMatches(name, labID, generation, key) {
			return key + ":management-tier", true
		}
	}
	// A response can be lost before either the server or policy is created.
	// Report the Tier as an advisory candidate without inventing a VM logical
	// label from its irreversible hash. Candidates never establish ownership.
	prefix := ktCloudTierBaseName(labID, generation) + "-m-"
	if strings.HasPrefix(name, prefix) && len(name) == len(prefix)+16 {
		digest, err := hex.DecodeString(strings.TrimPrefix(name, prefix))
		return "", err == nil && len(digest) == 8 && name == strings.ToLower(name)
	}
	legacy := provisionBaseName(labID, generation) + "-"
	return "", strings.HasPrefix(name, legacy) && strings.HasSuffix(name, "-management-tier") && len(name) > len(legacy)+len("-management-tier")
}
