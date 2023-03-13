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

package main

import (
	"context"
	"fmt"
	"os"

	"google.golang.org/grpc"

	verifierapi "github.com/containerd/containerd/api/services/verifier/v1"
	"github.com/containerd/containerd/sys"
)

func main() {
	// Provide a Unix socket address to listen on (the `address` in the `proxy_plugin`
	// configuration) followed by a space-separated list of allowed digests.
	// Example:
	//   go run main.go /run/example-verifier.sock sha256:ff6bdca1701f3a8a67e328815ff2346b0e4067d32ec36b7992c1fdc001dc8517 sha256:aa0afebbb3cfa473099a62c4b32e9b3fb73ed23f2a75a65ce1d4b4f55a5c2ef2
	if len(os.Args) < 2 {
		fmt.Printf("invalid args: usage: %s <unix addr> [allowed digest]...\n", os.Args[0])
		os.Exit(1)
	}

	socketPath := os.Args[1]

	allowedDigests := make(map[string]struct{})
	for _, digest := range os.Args[2:] {
		allowedDigests[digest] = struct{}{}
		fmt.Println("Allowed digest:", digest)
	}

	if len(allowedDigests) == 0 {
		fmt.Println("No digests are allowed")
	}

	// Create a gRPC server.
	rpc := grpc.NewServer()

	// Create the verifier service.
	service := &verifierServer{
		allowedDigests: allowedDigests,
	}

	// Register the service with the gRPC server.
	verifierapi.RegisterVerifierServer(rpc, service)

	// Listen on a socket at the given path.
	fmt.Printf("Listening on %v...\n", socketPath)
	lis, err := sys.GetLocalListener(socketPath, os.Geteuid(), os.Getegid())
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}

	// Listen and serve.
	if err := rpc.Serve(lis); err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}
}

type verifierServer struct {
	verifierapi.UnimplementedVerifierServer
	allowedDigests map[string]struct{}
}

func (s *verifierServer) VerifyImage(ctx context.Context, req *verifierapi.VerifyImageRequest) (*verifierapi.VerifyImageResponse, error) {
	fmt.Println("Verifying image", req.ImageName, "with digest", req.ImageDigest)
	if _, ok := s.allowedDigests[req.ImageDigest]; ok {
		return &verifierapi.VerifyImageResponse{
			OK: true,
		}, nil
	}

	return &verifierapi.VerifyImageResponse{
		OK:     false,
		Reason: "digest is not explicitly allowed",
	}, nil
}
