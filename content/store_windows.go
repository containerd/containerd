package content

import (
	"os"
	"time"
)

func getStartedAt(fi os.FileInfo) time.Time {
	return fi.ModTime()
}
