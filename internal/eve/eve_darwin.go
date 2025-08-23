package eve

import (
	"flag"
	"os"

	"github.com/secoba/elevate"
)

var sudo bool

func Elevate() {
	flag.BoolVar(&sudo, "sudo", false, "sudo")
	flag.Parse()
	if !sudo && os.Getenv("PLAYFAST_SUDO") == "" {
		_ = elevate.Command(os.Args[0], "-sudo", "true").Run()
		os.Exit(0)
	}
}
