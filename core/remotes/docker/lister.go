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

package docker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/containerd/errdefs"
	"github.com/containerd/log"
)

var ErrObjectNotRequired = errors.New("object not required")

type TagList struct {
	Name string   `json:"name"`
	Tags []string `json:"tags"`
}

type dockerLister struct {
	dockerBase *dockerBase
}

func (r *dockerLister) List(ctx context.Context) ([]string, error) {
	refSpec := r.dockerBase.refspec
	base := r.dockerBase
	var (
		errs  error
		paths [][]string
		caps  = HostCapabilityPull
	)

	// turns out, we have a valid digest, make an url.
	paths = append(paths, []string{"tags/list"})
	caps |= HostCapabilityResolve

	hosts := base.filterHosts(caps)
	if len(hosts) == 0 {
		return nil, fmt.Errorf("no list hosts: %w", errdefs.ErrNotFound)
	}

	ctx, err := ContextWithRepositoryScope(ctx, refSpec, false)
	if err != nil {
		return nil, err
	}

	// right now, this is a single path only. But we can add more variations.
	for _, u := range paths {
		for _, host := range hosts {
			ctxWithLogger := log.WithLogger(ctx, log.G(ctx).WithField("host", host.Host))

			req := base.request(host, http.MethodGet, u...)
			if err := req.addNamespace(base.refspec.Hostname()); err != nil {
				return nil, err
			}

			req.header["Accept"] = []string{"application/json"}

			log.G(ctxWithLogger).Debug("listing")

			data, ok := r.tryHost(ctxWithLogger, req, &errs, host)
			if !ok {
				log.G(ctxWithLogger).WithError(err).Debug("trying next host")

				continue
			}

			tags := &TagList{}
			if err = json.Unmarshal(data, tags); err != nil {
				return nil, err
			}

			return tags.Tags, nil
		}
	}

	// If above loop terminates without return, then there was an error.
	// "firstErr" contains the first non-404 error. That is, "firstErr == nil"
	// means that either no registries were given or each registry returned 404.

	if errs == nil {
		errs = fmt.Errorf("%s: %w", base.refspec.Locator, errdefs.ErrNotFound)
	}

	return nil, errs
}

// tryHost tries a host and sets the firstErr of we would like to have that set.
// In case of NotFound we are ignoring the error because we just want to continue and try the next host.
func (r *dockerLister) tryHost(ctx context.Context, req *request, firstErr *error, host RegistryHost) ([]byte, bool) {
	resp, err := req.doWithRetries(ctx, nil)
	if err != nil {
		if errors.Is(err, ErrInvalidAuthorization) {
			err = fmt.Errorf("pull access denied, repository does not exist or may require authorization: %w", err)
		}
		// Store the error for referencing later
		*firstErr = errors.Join(*firstErr, err)

		log.G(ctx).WithError(err).Info("trying next host")

		return nil, false
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		log.G(ctx).Info("trying next host - response was http.StatusNotFound")

		return nil, false
	}

	if resp.StatusCode != http.StatusOK {
		// Set firstErr when encountering the first non-404 status code.
		*firstErr = errors.Join(*firstErr, fmt.Errorf("pulling from host %s failed with status code: %v", host.Host, resp.Status))

		return nil, false
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		*firstErr = errors.Join(*firstErr, err)

		return nil, false
	}

	return data, true
}
