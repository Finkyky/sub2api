package main

import (
	"github.com/Wei-Shaw/sub2api/examples/general-plugins/internal/sample"
	general "github.com/Wei-Shaw/sub2api/pkg/pluginapi/general/v1"
	"os"
)

// The packager sets this using -ldflags "-X main.plugin=<example>".
var plugin = "credential-input"

func main() {
	s, e := sample.New(plugin)
	if e != nil {
		os.Exit(2)
	}
	general.Serve(s)
}
