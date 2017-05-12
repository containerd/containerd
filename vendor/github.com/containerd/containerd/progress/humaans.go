package progress

import (
	"fmt"
	"time"

	units "github.com/docker/go-units"
)

// Bytes converts a regular int64 to human readable type.
type Bytes int64

func (b Bytes) String() string {
	return units.CustomSize("%02.1f %s", float64(b), 1024.0, []string{"B", "KiB", "MiB", "GiB", "TiB", "PiB", "EiB", "ZiB", "YiB"})
}

type BytesPerSecond int64

func NewBytesPerSecond(n int64, duration time.Duration) BytesPerSecond {
	return BytesPerSecond(float64(n) / duration.Seconds())
}

func (bps BytesPerSecond) String() string {
	return fmt.Sprintf("%v/s", Bytes(bps))
}
