package allocdriver

import (
	"context"

	arstate "github.com/hashicorp/nomad/client/allocrunner/state"
	"github.com/hashicorp/nomad/client/taskenv"
	"github.com/hashicorp/nomad/nomad/structs"
	"github.com/hashicorp/nomad/plugins/base"
	"github.com/hashicorp/nomad/plugins/drivers"
	"github.com/hashicorp/nomad/plugins/shared/hclspec"
)

const (
	DriverHealthy = "Healthy"

	ApiVersion010 = "v0.1.0"
)

type TaskEvent = drivers.TaskEvent
type Fingerprint = drivers.Fingerprint
type AllocState = arstate.State

type NodeFingerprint struct {
	CpuShares int64
	MemoryMB  int64
	DiskMB    int64

	Attributes map[string]string
	Links      map[string]string
}

type TaskConfigParser interface {
	SetTaskEnvCustomizer(func(*taskenv.Builder))
	ParseTask(out interface{}, alloc *structs.Allocation, task *structs.Task) (env map[string]string, err error)
}

type ClientHandler interface {
	// AllocStateUpdated is used to emit an updated allocation. This allocation
	// is stripped to only include client settable fields.
	//AllocStateUpdated(alloc *structs.Allocation)

	GetAllocByID(allocID string) *structs.Allocation

	UpdateClientStatus(allocID string, state *AllocState) error

	TaskConfigParser(*hclspec.Spec) (TaskConfigParser, error)
}

type AllocDriverPlugin interface {
	base.BasePlugin

	Initialize(handler ClientHandler) error

	NodeFingerprint() (*NodeFingerprint, error)
	Fingerprint(context.Context) (<-chan *Fingerprint, error)
	TaskEvents(context.Context) (<-chan *TaskEvent, error)

	StartAllocation(alloc *structs.Allocation) error
	StopAllocation(allocID string) error
	UpdateAllocation(alloc *structs.Allocation) error

	SignalAllocation(allocID, taskName, signal string) error
	RestartAllocation(allocID, taskName string) error
	InspectAllocation(allocID string) (*AllocState, error)

	Shutdown()
}
