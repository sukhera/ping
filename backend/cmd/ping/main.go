// Command ping runs the ping API server and background workers.
package main

import (
	"fmt"
	"os"
)

func main() {
	if _, err := fmt.Fprintln(os.Stdout, "ping: scaffold only, see PING-003 for server wiring"); err != nil {
		os.Exit(1)
	}
}
