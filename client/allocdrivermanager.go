package client

import (
	"errors"
	"fmt"

	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/go-multierror"
	"github.com/hashicorp/hcl2/hcldec"
	"github.com/hashicorp/nomad/client/allocdir"
	"github.com/hashicorp/nomad/client/allocrunner/interfaces"
	arstate "github.com/hashicorp/nomad/client/allocrunner/state"
	"github.com/hashicorp/nomad/client/config"
	"github.com/hashicorp/nomad/client/pluginmanager/drivermanager"
	"github.com/hashicorp/nomad/client/state"
	"github.com/hashicorp/nomad/client/taskenv"
	"github.com/hashicorp/nomad/helper/pluginutils/hclspecutils"
	"github.com/hashicorp/nomad/helper/pluginutils/hclutils"
	"github.com/hashicorp/nomad/nomad/structs"
	"github.com/hashicorp/nomad/plugins/allocdriver"
	"github.com/hashicorp/nomad/plugins/base"
	"github.com/hashicorp/nomad/plugins/drivers"
	"github.com/hashicorp/nomad/plugins/shared/hclspec"
	"github.com/zclconf/go-cty/cty/msgpack"
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

func (m *adManager) TaskConfigParser(schema *hclspec.Spec) (allocdriver.TaskConfigParser, error) {
	spec, diag := hclspecutils.Convert(schema)
	if diag.HasErrors() {
		return nil, fmt.Errorf("failed to convert task schema: %v", diag)
	}

	return &configParser{
		region:     m.client.Region(),
		node:       m.client.Node(),
		taskSchema: spec,
	}, nil

}

type configParser struct {
	region     string
	node       *structs.Node
	taskSchema hcldec.Spec

	envCustomizer func(*taskenv.Builder)
}

func (p *configParser) SetTaskEnvCustomizer(fn func(*taskenv.Builder)) {
	p.envCustomizer = fn

}
func (p *configParser) ParseTask(out interface{}, alloc *structs.Allocation, task *structs.Task) (map[string]string, error) {
	envBuilder := taskenv.NewBuilder(p.node, alloc, task, p.region)

	if p.envCustomizer != nil {
		p.envCustomizer(envBuilder)
	}

	env := envBuilder.Build()
	vars, _, err := env.AllValues()
	if err != nil {
		return nil, fmt.Errorf("failed to compute env vars: %v", err)
	}

	val, _, diagErrs := hclutils.ParseHclInterface(task.Config, p.taskSchema, vars)
	if len(diagErrs) != 0 {
		return nil, multierror.Append(errors.New("failed to parse config: "), diagErrs...)
	}

	data, err := msgpack.Marshal(val, val.Type())
	if err != nil {
		return nil, fmt.Errorf("failed to marshal data: %v", err)
	}

	err = base.MsgPackDecode(data, out)
	if err != nil {
		return nil, fmt.Errorf("failed to decode data: %v", err)
	}

	return env.All(), nil
}
