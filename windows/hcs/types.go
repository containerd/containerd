// +build windows

package hcs

import "time"

type Configuration struct {
	TerminateDuration time.Duration `json:"terminateDuration,omitempty"`
}
