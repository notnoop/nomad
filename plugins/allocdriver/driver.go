package allocdriver

import (
	"context"

	arstate "github.com/hashicorp/nomad/client/allocrunner/state"
	"github.com/hashicorp/nomad/nomad/structs"
	"github.com/hashicorp/nomad/plugins/base"
	"github.com/hashicorp/nomad/plugins/drivers"
)

const (
	DriverHealthy = "Healthy"
)

type Fingerprint = drivers.Fingerprint
type AllocState = arstate.State

type NodeFingerprint struct {
	CpuShares int64
	MemoryMB  int64
	DiskMB    int64

	Attributes map[string]string
	Links      map[string]string
}

type AllocDriverPlugin interface {
	base.BasePlugin

	NodeFingerprint() (*NodeFingerprint, error)
	Fingerprint(context.Context) (<-chan *Fingerprint, error)

	StartAllocation(alloc *structs.Allocation) error
	StopAllocation(allocID string) error
	UpdateAllocation(alloc *structs.Allocation) error

	SignalAllocation(allocID, taskName, signal string) error
	RestartAllocation(allocID, taskName string) error
	InspectAllocation(allocID string) (*AllocState, error)

	// returns all allocations IDs this is aware mapped to alloc.AllocModifyIndex
	RunningAllocations() (map[string]uint64, error)
}
