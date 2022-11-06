//go:build windows

package runhcs

import (
	"context"
	"encoding/json"
	"fmt"
)

// Ps displays the processes running inside a container.
func (r *Runhcs) Ps(context context.Context, id string) ([]int, error) {
	data, err := cmdOutput(r.command(context, "ps", "--format=json", id), true)
	if err != nil {
		return nil, fmt.Errorf("%s: %s", err, data)
	}
	var out []int
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	return out, nil
}
