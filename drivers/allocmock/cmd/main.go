package main

import (
	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/nomad/drivers/allocmock"
	allocdriverlauncher "github.com/hashicorp/nomad/plugins/allocdriver/launcher"
)

func main() {
	allocdriverlauncher.Serve(newDriver)
}

func newDriver(logger hclog.Logger) interface{} {
	return allocmock.New(logger)
}
