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

package auth

import (
	"context"
	"errors"
	"io"
	"sync"

	transfertypes "github.com/containerd/containerd/api/types/transfer"
	"github.com/containerd/containerd/v2/core/streaming"
	"github.com/containerd/containerd/v2/core/transfer"
	"github.com/containerd/log"
	"github.com/containerd/typeurl/v2"
)

// From stream
type CredentialHelper interface {
	GetCredentials(ctx context.Context, ref, host string) (transfer.Credentials, error)
}

type credentialHelper struct {
	sync.Mutex
	stream streaming.Stream
}

func NewCredentialHelper(stream streaming.Stream) CredentialHelper {
	return &credentialHelper{stream: stream}
}

func (cc *credentialHelper) GetCredentials(ctx context.Context, ref, host string) (transfer.Credentials, error) {
	cc.Lock()
	defer cc.Unlock()

	ar := &transfertypes.AuthRequest{
		Host:      host,
		Reference: ref,
	}
	anyType, err := typeurl.MarshalAny(ar)
	if err != nil {
		return transfer.Credentials{}, err
	}
	if err := cc.stream.Send(anyType); err != nil {
		return transfer.Credentials{}, err
	}
	resp, err := cc.stream.Recv()
	if err != nil {
		return transfer.Credentials{}, err
	}
	var s transfertypes.AuthResponse
	if err := typeurl.UnmarshalTo(resp, &s); err != nil {
		return transfer.Credentials{}, err
	}
	creds := transfer.Credentials{
		Host: host,
	}
	switch s.AuthType {
	case transfertypes.AuthType_CREDENTIALS:
		creds.Username = s.Username
		creds.Secret = s.Secret
	case transfertypes.AuthType_REFRESH:
		creds.Secret = s.Secret
	case transfertypes.AuthType_HEADER:
		creds.Header = s.Secret
	}

	return creds, nil
}

func ServeAuthStream(ctx context.Context, stream streaming.Stream, getCredentials func(ctx context.Context, ref, host string) (transfer.Credentials, error)) {
	// Check for context cancellation as well
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		req, err := stream.Recv()
		if err != nil {
			// If not EOF, log error
			return
		}

		var s transfertypes.AuthRequest
		if err := typeurl.UnmarshalTo(req, &s); err != nil {
			log.G(ctx).WithError(err).Error("failed to unmarshal credential request")
			continue
		}
		creds, err := getCredentials(ctx, s.Reference, s.Host)
		if err != nil {
			log.G(ctx).WithError(err).Error("failed to get credentials")
			continue
		}
		var resp transfertypes.AuthResponse
		if creds.Header != "" {
			resp.AuthType = transfertypes.AuthType_HEADER
			resp.Secret = creds.Header
		} else if creds.Username != "" {
			resp.AuthType = transfertypes.AuthType_CREDENTIALS
			resp.Username = creds.Username
			resp.Secret = creds.Secret
		} else {
			resp.AuthType = transfertypes.AuthType_REFRESH
			resp.Secret = creds.Secret
		}

		a, err := typeurl.MarshalAny(&resp)
		if err != nil {
			log.G(ctx).WithError(err).Error("failed to marshal credential response")
			continue
		}

		if err := stream.Send(a); err != nil {
			if !errors.Is(err, io.EOF) {
				log.G(ctx).WithError(err).Error("unexpected send failure")
			}
			return
		}
	}
}
