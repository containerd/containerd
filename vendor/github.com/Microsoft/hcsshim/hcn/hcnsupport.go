package hcn

import (
	"fmt"
	"sync"

	"github.com/sirupsen/logrus"
)

var versionOnce sync.Once

// SupportedFeatures are the features provided by the Service.
type SupportedFeatures struct {
	Acl                      AclFeatures `json:"ACL"`
	Api                      ApiSupport  `json:"API"`
	RemoteSubnet             bool        `json:"RemoteSubnet"`
	HostRoute                bool        `json:"HostRoute"`
	DSR                      bool        `json:"DSR"`
	Slash32EndpointPrefixes  bool        `json:"Slash32EndpointPrefixes"`
	AclSupportForProtocol252 bool        `json:"AclSupportForProtocol252"`
	SessionAffinity          bool        `json:"SessionAffinity"`
	IPv6DualStack            bool        `json:"IPv6DualStack"`
	SetPolicy                bool        `json:"SetPolicy"`
	VxlanPort                bool        `json:"VxlanPort"`
	L4Proxy                  bool        `json:"L4Proxy"`    // network policy that applies VFP rules to all endpoints on the network to redirect traffic
	L4WfpProxy               bool        `json:"L4WfpProxy"` // endpoint policy that applies WFP filters to redirect traffic to/from that endpoint
	TierAcl                  bool        `json:"TierAcl"`
	NetworkACL               bool        `json:"NetworkACL"`
	NestedIpSet              bool        `json:"NestedIpSet"`
}

// AclFeatures are the supported ACL possibilities.
type AclFeatures struct {
	AclAddressLists       bool `json:"AclAddressLists"`
	AclNoHostRulePriority bool `json:"AclHostRulePriority"`
	AclPortRanges         bool `json:"AclPortRanges"`
	AclRuleId             bool `json:"AclRuleId"`
}

// ApiSupport lists the supported API versions.
type ApiSupport struct {
	V1 bool `json:"V1"`
	V2 bool `json:"V2"`
}

// GetSupportedFeatures returns the features supported by the Service.
func GetSupportedFeatures() SupportedFeatures {
	var features SupportedFeatures

	globals, err := GetGlobals()
	if err != nil {
		// Expected on pre-1803 builds, all features will be false/unsupported
		logrus.Debugf("Unable to obtain globals: %s", err)
		return features
	}

	features.Acl = AclFeatures{
		AclAddressLists:       isFeatureSupported(globals.Version, HNSVersion1803),
		AclNoHostRulePriority: isFeatureSupported(globals.Version, HNSVersion1803),
		AclPortRanges:         isFeatureSupported(globals.Version, HNSVersion1803),
		AclRuleId:             isFeatureSupported(globals.Version, HNSVersion1803),
	}

	features.Api = ApiSupport{
		V2: isFeatureSupported(globals.Version, V2ApiSupport),
		V1: true, // HNSCall is still available.
	}

	features.RemoteSubnet = isFeatureSupported(globals.Version, RemoteSubnetVersion)
	features.HostRoute = isFeatureSupported(globals.Version, HostRouteVersion)
	features.DSR = isFeatureSupported(globals.Version, DSRVersion)
	features.Slash32EndpointPrefixes = isFeatureSupported(globals.Version, Slash32EndpointPrefixesVersion)
	features.AclSupportForProtocol252 = isFeatureSupported(globals.Version, AclSupportForProtocol252Version)
	features.SessionAffinity = isFeatureSupported(globals.Version, SessionAffinityVersion)
	features.IPv6DualStack = isFeatureSupported(globals.Version, IPv6DualStackVersion)
	features.SetPolicy = isFeatureSupported(globals.Version, SetPolicyVersion)
	features.VxlanPort = isFeatureSupported(globals.Version, VxlanPortVersion)
	features.L4Proxy = isFeatureSupported(globals.Version, L4ProxyPolicyVersion)
	features.L4WfpProxy = isFeatureSupported(globals.Version, L4WfpProxyPolicyVersion)
	features.TierAcl = isFeatureSupported(globals.Version, TierAclPolicyVersion)
	features.NetworkACL = isFeatureSupported(globals.Version, NetworkACLPolicyVersion)
	features.NestedIpSet = isFeatureSupported(globals.Version, NestedIpSetVersion)

	// Only print the HCN version and features supported once, instead of everytime this is invoked. These logs are useful to
	// debug incidents where there's confusion on if a feature is supported on the host machine. The sync.Once helps to avoid redundant
	// spam of these anytime a check needs to be made for if an HCN feature is supported. This is a common occurrence in kubeproxy
	// for example.
	versionOnce.Do(func() {
		logrus.WithFields(logrus.Fields{
			"version":           fmt.Sprintf("%+v", globals.Version),
			"supportedFeatures": fmt.Sprintf("%+v", features),
		}).Info("HCN feature check")
	})

	return features
}

func isFeatureSupported(currentVersion Version, versionsSupported VersionRanges) bool {
	isFeatureSupported := false

	for _, versionRange := range versionsSupported {
		isFeatureSupported = isFeatureSupported || isFeatureInRange(currentVersion, versionRange)
	}

	return isFeatureSupported
}

func isFeatureInRange(currentVersion Version, versionRange VersionRange) bool {
	if currentVersion.Major < versionRange.MinVersion.Major {
		return false
	}
	if currentVersion.Major > versionRange.MaxVersion.Major {
		return false
	}
	if currentVersion.Major == versionRange.MinVersion.Major && currentVersion.Minor < versionRange.MinVersion.Minor {
		return false
	}
	if currentVersion.Major == versionRange.MaxVersion.Major && currentVersion.Minor > versionRange.MaxVersion.Minor {
		return false
	}
	return true
}
