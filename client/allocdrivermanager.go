package client

import (
	"fmt"

	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/nomad/client/allocdir"
	"github.com/hashicorp/nomad/client/allocrunner/interfaces"
	arstate "github.com/hashicorp/nomad/client/allocrunner/state"
	"github.com/hashicorp/nomad/client/pluginmanager/drivermanager"
	"github.com/hashicorp/nomad/client/state"
	"github.com/hashicorp/nomad/nomad/structs"
	"github.com/hashicorp/nomad/plugins/allocdriver"
	"github.com/hashicorp/nomad/plugins/drivers"
)

type adManager struct {
	logger hclog.Logger

	client *Client

	// stateDB is used to efficiently store client state.
	stateDB state.StateDB

	driver allocdriver.AllocDriverPlugin
}

var _ AllocManager = (*adManager)(nil)

func (m *adManager) SignalAllocation(allocID, task, signal string) error {
	return m.driver.SignalAllocation(allocID, task, signal)

}
func (m *adManager) RestartAllocation(allocID, taskName string) error {
	return m.driver.RestartAllocation(allocID, taskName)
}
func (m *adManager) GetAllocStats(allocID string) (interfaces.AllocStatsReporter, error) {
	return nil, fmt.Errorf("not supported")
}

func (m *adManager) GetAllocFS(allocID string) (allocdir.AllocDirFS, error) {
	return nil, fmt.Errorf("not supported")
}

func (m *adManager) GetAllocState(allocID string) (*arstate.State, error) {
	return nil, fmt.Errorf("not supported")
}

func (m *adManager) GetTaskEventHandler(allocID, taskName string) drivermanager.EventHandler {
	return func(*drivers.TaskEvent) {}

}

// exec specific
func (m *adManager) GetTaskExecHandler(allocID, task string) (drivermanager.TaskExecHandler, *drivers.Capabilities, error) {
	return nil, nil, fmt.Errorf("not supported")
}

// stats
func (m *adManager) getAllocatedResources(selfNode *structs.Node) *structs.ComparableResources {
	return &structs.ComparableResources{}
}

func (m *adManager) clientMetrics() *clientMetrics {
	return &clientMetrics{}
}

// callbacks
func (m *adManager) allocTerminated(allocID string) {
}

// general lifecycle functions
func (m *adManager) restoreState(allocs []*structs.Allocation) error {
	return fmt.Errorf("not supported")
}
func (m *adManager) saveState() error {
	return fmt.Errorf("not supported")
}

func (m *adManager) init(c *Client) {

}

func (m *adManager) destroy() error {
	return fmt.Errorf("not supported")
}

func (m *adManager) shutdown() error {
	return fmt.Errorf("not supported")
}

func (m *adManager) NumAllocs() int {
	return 0
}

func (m *adManager) allocSynchronizer() (AllocSynchronizer, error) {
	return nil, fmt.Errorf("not supported")
}
