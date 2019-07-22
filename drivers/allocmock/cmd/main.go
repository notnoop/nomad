package main

import (
	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/nomad/drivers/allocmock"
	"github.com/hashicorp/nomad/plugins/allocdriver"
)

func main() {
	allocdriver.Serve(newDriver)
}

func newDriver(logger hclog.Logger) interface{} {
	return allocmock.New(logger)
}
