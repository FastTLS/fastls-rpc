package testprocess

import (
	"os"
	"testing"
	"time"
)

const helperExitMarker = "testprocess-helper-exit"

func TestProcessWaitsForNaturalExit(t *testing.T) {
	process := Start(t, os.Args[0], "-test.run=^TestProcessHelperExit$", "--", helperExitMarker)
	if err := process.Wait(2 * time.Second); err != nil {
		t.Fatalf("wait for helper process: %v\n%s", err, process.Logs())
	}
}

func TestProcessHelperExit(t *testing.T) {
	for _, argument := range os.Args {
		if argument == helperExitMarker {
			os.Exit(0)
		}
	}
}
