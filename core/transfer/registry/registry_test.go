/*
   Copyright The containerd Authors.

   Licensed under the Apache License, Version 2.0 (the "License");
   you may not use this file except in compliance with the License.
   You may obtain a copy of the License at

       http://www.apache.org/licenses/LICENSE-2.0

   Unless required by applicable law or agreed to in writing, software
   distributed under the License is distributed on an "AS IS" BASIS,
   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
   See the License for the specific language governing permissions and
   limitations under the License.
*/

package registry

import (
	"context"
	"slices"
	"testing"

	transfertypes "github.com/containerd/containerd/api/types/transfer"
	"github.com/containerd/typeurl/v2"
)

func TestOCIRegistryDNSServersRoundTrip(t *testing.T) {
	servers := []string{"192.0.2.53", "2001:db8::53"}
	reg, err := NewOCIRegistry(context.Background(), "registry.example.com/library/test:latest", WithDNSServers(servers))
	if err != nil {
		t.Fatal(err)
	}

	encoded, err := reg.MarshalAny(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}

	var serialized transfertypes.OCIRegistry
	if err := typeurl.UnmarshalTo(encoded, &serialized); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(serialized.Resolver.DnsServers, servers) {
		t.Fatalf("serialized DNS servers = %v, want %v", serialized.Resolver.DnsServers, servers)
	}

	var decoded OCIRegistry
	if err := decoded.UnmarshalAny(context.Background(), nil, encoded); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(decoded.dnsServers, servers) {
		t.Fatalf("decoded DNS servers = %v, want %v", decoded.dnsServers, servers)
	}
}
