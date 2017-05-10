// +build windows

package hcs

import "time"

type Configuration struct {
	UseHyperV bool `json:"useHyperV,omitempty"`

	Layers []string `json:"layers"`

	TerminateDuration time.Duration `json:"terminateDuration,omitempty"`

	IgnoreFlushesDuringBoot bool `json:"ignoreFlushesDuringBoot,omitempty"`

	AllowUnqualifiedDNSQuery bool     `json:"allowUnqualifiedDNSQuery,omitempty"`
	DNSSearchList            []string `json:"dnsSearchList,omitempty"`
	NetworkEndpoints         []string `json:"networkEndpoints,omitempty"`
	NetworkSharedContainerID string

	Credentials string `json:"credentials,omitempty"`
}
