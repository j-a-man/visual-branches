package cli

import (
	"fmt"
	"os"
	"time"
)

var debugStart = time.Now()

// trace logs a timestamped step when VB_DEBUG is set.
func trace(step string) {
	if os.Getenv("VB_DEBUG") != "" {
		fmt.Fprintf(os.Stderr, "vb: %7.1fms %s\n", float64(time.Since(debugStart).Microseconds())/1000, step)
	}
}
