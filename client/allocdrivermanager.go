package client

import (
	"fmt"

	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/nomad/client/allocdir"
	"github.com/hashicorp/nomad/client/allocrunner/interfaces"
	arstate "github.com/hashicorp/nomad/client/allocrunner/state"
	"github.com/hashicorp/nomad/client/config"
	"github.com/hashicorp/nomad/client/pluginmanager/drivermanager"
	"github.com/hashicorp/nomad/client/state"
	"github.com/hashicorp/nomad/nomad/structs"
	"github.com/hashicorp/nomad/plugins/allocdriver"
	"github.com/hashicorp/nomad/plugins/base"
	"github.com/hashicorp/nomad/plugins/drivers"
)

type adManager struct {
	logger hclog.Logger

	client *Client

	// stateDB is used to efficiently store client state.
	stateDB state.StateDB

	driver allocdriver.AllocDriverPlugin

	runningAllocs map[string]*structs.Allocation
}

func newAllocDriverManager(cfg *config.Config, logger hclog.Logger) (*adManager, error) {
	allocDrivers := cfg.PluginLoader.Catalog()[base.PluginTypeAllocDriver]
	if len(allocDrivers) == 0 {
		return nil, nil
	}

	d := allocDrivers[0]
	plugin, err := cfg.PluginLoader.Dispense(d.Name, d.Type, nil, logger)
	if err != nil {
		return nil, err
	}

	return &adManager{
		logger:        logger,
		driver:        plugin.Plugin().(allocdriver.AllocDriverPlugin),
		runningAllocs: map[string]*structs.Allocation{},
	}, nil
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
	m.client = c
	m.driver.Initialize(m)
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
	return m, nil
}

func (m *adManager) runningAllocations() map[string]uint64 {
	allocs := make(map[string]uint64, len(m.runningAllocs))
	for _, alloc := range m.runningAllocs {
		allocs[alloc.ID] = alloc.AllocModifyIndex
	}

	return allocs
}

func (m *adManager) isInvalidAlloc(allocID string) bool {
	return false
}

func (m *adManager) removeAlloc(allocID string) {
	m.driver.StopAllocation(allocID)
}
func (m *adManager) updateAlloc(alloc *structs.Allocation) {
	m.driver.UpdateAllocation(alloc)
}

func (m *adManager) startAlloc(alloc *structs.Allocation, migrationToke string) error {
	m.runningAllocs[alloc.ID] = alloc
	return m.driver.StartAllocation(alloc)
}

func (m *adManager) GetAllocByID(allocID string) *structs.Allocation {
	return m.runningAllocs[allocID]
}

func (m *adManager) UpdateClientStatus(allocID string, state *allocdriver.AllocState) error {
	alloc, ok := m.runningAllocs[allocID]
	if !ok {
		return structs.NewErrUnknownAllocation(allocID)
	}

	a := alloc.CopySkipJob()
	a.ClientStatus = state.ClientStatus
	a.ClientDescription = state.ClientDescription
	a.TaskStates = state.TaskStates
	m.logger.Info("updating alloc", "alloc_id", a.ID, "states", a.TaskStates)
	m.client.AllocStateUpdated(a)
	return nil
}
