package client

import (
	"net"
	"sync"

	hclog "github.com/hashicorp/go-hclog"
	multierror "github.com/hashicorp/go-multierror"
	"github.com/hashicorp/nomad/client/allocdir"
	"github.com/hashicorp/nomad/client/allocrunner"
	"github.com/hashicorp/nomad/client/allocrunner/interfaces"
	arstate "github.com/hashicorp/nomad/client/allocrunner/state"
	"github.com/hashicorp/nomad/client/allocwatcher"
	"github.com/hashicorp/nomad/client/pluginmanager/drivermanager"
	"github.com/hashicorp/nomad/client/state"
	"github.com/hashicorp/nomad/nomad/structs"
	"github.com/hashicorp/nomad/plugins/drivers"
)

type clientMetrics struct {
	blocked   uint
	migrating uint
	pending   uint
	running   uint
	terminal  uint
}
type AllocSynchronizer interface {
	runningAllocations() map[string]uint64
	isInvalidAlloc(allocID string) bool

	removeAlloc(allocID string)
	updateAlloc(alloc *structs.Allocation)
	startAlloc(alloc *structs.Allocation, migrationToke string) error
}

type AllocManager interface {
	// Alloc lifecycle
	SignalAllocation(allocID, task, signal string) error
	RestartAllocation(allocID, taskName string) error
	GetAllocStats(allocID string) (interfaces.AllocStatsReporter, error)
	GetAllocFS(allocID string) (allocdir.AllocDirFS, error)
	GetAllocState(allocID string) (*arstate.State, error)
	GetTaskEventHandler(allocID, taskName string) drivermanager.EventHandler

	// exec specific
	GetTaskExecHandler(allocID, task string) (drivermanager.TaskExecHandler, *drivers.Capabilities, error)

	// stats
	getAllocatedResources(selfNode *structs.Node) *structs.ComparableResources
	clientMetrics() *clientMetrics

	// callbacks
	allocTerminated(allocID string)

	// general lifecycle functions
	restoreState(allocs []*structs.Allocation) error
	saveState() error

	init(c *Client)
	destroy() error
	shutdown() error
	NumAllocs() int

	allocSynchronizer() (AllocSynchronizer, error)
}

type arManager struct {
	logger hclog.Logger

	allocs    map[string]AllocRunner
	allocLock sync.RWMutex

	invalidAllocs     map[string]struct{}
	invalidAllocsLock sync.Mutex

	client *Client

	// stateDB is used to efficiently store client state.
	stateDB state.StateDB

	// garbageCollector is used to garbage collect terminal allocations present
	// in the node automatically
	garbageCollector *AllocGarbageCollector
}

func (m *arManager) init(c *Client) {
	m.client = c
	m.stateDB = c.stateDB
	m.garbageCollector = c.garbageCollector
}

func (c *arManager) destroy() error {
	arGroup := group{}
	// In DevMode destroy all the running allocations.
	for _, ar := range c.getAllocRunners() {
		ar.Destroy()
		arGroup.AddCh(ar.DestroyCh())
	}
	arGroup.Wait()
	return nil

}

func (c *arManager) shutdown() error {
	arGroup := group{}
	// In DevMode destroy all the running allocations.
	for _, ar := range c.getAllocRunners() {
		ar.Shutdown()
		arGroup.AddCh(ar.ShutdownCh())
	}
	arGroup.Wait()
	return nil

}

func (c *arManager) getAllocRunners() map[string]AllocRunner {
	c.allocLock.RLock()
	defer c.allocLock.RUnlock()

	runners := make(map[string]AllocRunner, len(c.allocs))
	for id, ar := range c.allocs {
		runners[id] = ar
	}
	return runners
}

// NumAllocs returns the number of un-GC'd allocs this client has. Used to
// fulfill the AllocCounter interface for the GC.
func (c *arManager) NumAllocs() int {
	c.allocLock.RLock()
	n := 0
	for _, a := range c.allocs {
		if !a.IsDestroyed() {
			n++
		}
	}
	c.allocLock.RUnlock()
	return n
}

func (c *arManager) getAllocRunner(allocID string) (AllocRunner, error) {
	c.allocLock.RLock()
	defer c.allocLock.RUnlock()

	ar, ok := c.allocs[allocID]
	if !ok {
		return nil, structs.NewErrUnknownAllocation(allocID)
	}

	return ar, nil
}

// SignalAllocation sends a signal to the tasks within an allocation.
// If the provided task is empty, then every allocation will be signalled.
// If a task is provided, then only an exactly matching task will be signalled.
func (c *arManager) SignalAllocation(allocID, task, signal string) error {
	ar, err := c.getAllocRunner(allocID)
	if err != nil {
		return err
	}

	return ar.Signal(task, signal)
}

func (c *arManager) RestartAllocation(allocID, taskName string) error {
	ar, err := c.getAllocRunner(allocID)
	if err != nil {
		return err
	}

	event := structs.NewTaskEvent(structs.TaskRestartSignal).
		SetRestartReason("User requested restart")

	if taskName != "" {
		return ar.RestartTask(taskName, event)
	}

	return ar.RestartAll(event)
}

func (c *arManager) GetAllocStats(allocID string) (interfaces.AllocStatsReporter, error) {
	ar, err := c.getAllocRunner(allocID)
	if err != nil {
		return nil, err
	}
	return ar.StatsReporter(), nil
}

// GetAllocFS returns the AllocFS interface for the alloc dir of an allocation
func (c *arManager) GetAllocFS(allocID string) (allocdir.AllocDirFS, error) {
	ar, err := c.getAllocRunner(allocID)
	if err != nil {
		return nil, err
	}

	return ar.GetAllocDir(), nil
}

// GetAllocState returns a copy of an allocation's state on this client. It
// returns either an AllocState or an unknown allocation error.
func (c *arManager) GetAllocState(allocID string) (*arstate.State, error) {
	ar, err := c.getAllocRunner(allocID)
	if err != nil {
		return nil, err
	}

	return ar.AllocState(), nil
}

// GetTaskEventHandler returns an event handler for the given allocID and task name
func (c *arManager) GetTaskEventHandler(allocID, taskName string) drivermanager.EventHandler {
	c.allocLock.RLock()
	defer c.allocLock.RUnlock()
	if ar, ok := c.allocs[allocID]; ok {
		return ar.GetTaskEventHandler(taskName)
	}
	return nil
}

func (m *arManager) GetTaskExecHandler(allocID, taskName string) (drivermanager.TaskExecHandler, *drivers.Capabilities, error) {
	ar, err := m.getAllocRunner(allocID)
	if err != nil {
		return nil, nil, err
	}

	caps, err := ar.GetTaskDriverCapabilities(taskName)
	if err != nil {
		return nil, nil, err
	}

	return ar.GetTaskExecHandler(taskName), caps, nil
}

func (c *arManager) allocTerminated(allocID string) {
	// Terminated, mark for GC if we're still tracking this alloc
	// runner. If it's not being tracked that means the server has
	// already GC'd it (see removeAlloc).
	ar, err := c.getAllocRunner(allocID)
	if err != nil {
		return
	}

	c.garbageCollector.MarkForCollection(allocID, ar)

	// Trigger a GC in case we're over thresholds and just
	// waiting for eligible allocs.
	c.garbageCollector.Trigger()
}

func (m *arManager) restoreState(allocs []*structs.Allocation) error {
	c := m.client
	for _, alloc := range allocs {

		// COMPAT(0.12): remove once upgrading from 0.9.5 is no longer supported
		// See hasLocalState for details.  Skipping suspicious allocs
		// now.  If allocs should be run, they will be started when the client
		// gets allocs from servers.
		if !c.hasLocalState(alloc) {
			c.logger.Warn("found a alloc without any local state, skipping restore", "alloc_id", alloc.ID)
			continue
		}

		//XXX On Restore we give up on watching previous allocs because
		//    we need the local AllocRunners initialized first. We could
		//    add a second loop to initialize just the alloc watcher.
		prevAllocWatcher := allocwatcher.NoopPrevAlloc{}
		prevAllocMigrator := allocwatcher.NoopPrevAlloc{}

		c.configLock.RLock()
		arConf := &allocrunner.Config{
			Alloc:               alloc,
			Logger:              c.logger,
			ClientConfig:        c.configCopy,
			StateDB:             c.stateDB,
			StateUpdater:        c,
			DeviceStatsReporter: c,
			Consul:              c.consulService,
			Vault:               c.vaultClient,
			PrevAllocWatcher:    prevAllocWatcher,
			PrevAllocMigrator:   prevAllocMigrator,
			DeviceManager:       c.devicemanager,
			DriverManager:       c.drivermanager,
			ServersContactedCh:  c.serversContactedCh,
		}
		c.configLock.RUnlock()

		ar, err := allocrunner.NewAllocRunner(arConf)
		if err != nil {
			c.logger.Error("error running alloc", "error", err, "alloc_id", alloc.ID)
			c.handleInvalidAllocs(alloc, err)
			continue
		}

		// Restore state
		if err := ar.Restore(); err != nil {
			c.logger.Error("error restoring alloc", "error", err, "alloc_id", alloc.ID)
			// Override the status of the alloc to failed
			ar.SetClientStatus(structs.AllocClientStatusFailed)
			// Destroy the alloc runner since this is a failed restore
			ar.Destroy()
			continue
		}

		//XXX is this locking necessary?
		m.allocLock.Lock()
		m.allocs[alloc.ID] = ar
		m.allocLock.Unlock()
	}

	// All allocs restored successfully, run them!
	m.allocLock.Lock()
	for _, ar := range m.allocs {
		go ar.Run()
	}
	m.allocLock.Unlock()
	return nil

}

func (m *arManager) saveState() error {
	var wg sync.WaitGroup
	var l sync.Mutex
	var mErr multierror.Error
	runners := m.getAllocRunners()
	wg.Add(len(runners))

	for id, ar := range runners {
		go func(id string, ar AllocRunner) {
			err := ar.PersistState()
			if err != nil {
				m.logger.Error("error saving alloc state", "error", err, "alloc_id", id)
				l.Lock()
				multierror.Append(&mErr, err)
				l.Unlock()
			}
			wg.Done()
		}(id, ar)
	}

	wg.Wait()
	return mErr.ErrorOrNil()
}

func (m *arManager) getAllocatedResources(selfNode *structs.Node) *structs.ComparableResources {
	// Unfortunately the allocs only have IP so we need to match them to the
	// device
	cidrToDevice := make(map[*net.IPNet]string, len(selfNode.Resources.Networks))
	for _, n := range selfNode.NodeResources.Networks {
		_, ipnet, err := net.ParseCIDR(n.CIDR)
		if err != nil {
			continue
		}
		cidrToDevice[ipnet] = n.Device
	}

	// Sum the allocated resources
	var allocated structs.ComparableResources
	allocatedDeviceMbits := make(map[string]int)
	for _, ar := range m.getAllocRunners() {
		alloc := ar.Alloc()
		if alloc.ServerTerminalStatus() || ar.AllocState().ClientTerminalStatus() {
			continue
		}

		// Add the resources
		// COMPAT(0.11): Just use the allocated resources
		allocated.Add(alloc.ComparableResources())

		// Add the used network
		if alloc.AllocatedResources != nil {
			for _, tr := range alloc.AllocatedResources.Tasks {
				for _, allocatedNetwork := range tr.Networks {
					for cidr, dev := range cidrToDevice {
						ip := net.ParseIP(allocatedNetwork.IP)
						if cidr.Contains(ip) {
							allocatedDeviceMbits[dev] += allocatedNetwork.MBits
							break
						}
					}
				}
			}
		} else if alloc.Resources != nil {
			for _, allocatedNetwork := range alloc.Resources.Networks {
				for cidr, dev := range cidrToDevice {
					ip := net.ParseIP(allocatedNetwork.IP)
					if cidr.Contains(ip) {
						allocatedDeviceMbits[dev] += allocatedNetwork.MBits
						break
					}
				}
			}
		}
	}

	// Clear the networks
	allocated.Flattened.Networks = nil
	for dev, speed := range allocatedDeviceMbits {
		net := &structs.NetworkResource{
			Device: dev,
			MBits:  speed,
		}
		allocated.Flattened.Networks = append(allocated.Flattened.Networks, net)
	}

	return &allocated

}

func (m *arManager) clientMetrics() *clientMetrics {
	c := clientMetrics{}
	for _, ar := range m.getAllocRunners() {
		switch ar.AllocState().ClientStatus {
		case structs.AllocClientStatusPending:
			switch {
			case ar.IsWaiting():
				c.blocked++
			case ar.IsMigrating():
				c.migrating++
			default:
				c.pending++
			}
		case structs.AllocClientStatusRunning:
			c.running++
		case structs.AllocClientStatusComplete, structs.AllocClientStatusFailed:
			c.terminal++
		}
	}

	return &c
}

func (m *arManager) allocSynchronizer() (AllocSynchronizer, error) {
	return m, nil
}

func (c *arManager) runningAllocations() map[string]uint64 {
	c.allocLock.RLock()
	existing := make(map[string]uint64, len(c.allocs))
	for id, ar := range c.allocs {
		existing[id] = ar.Alloc().AllocModifyIndex
	}
	c.allocLock.RUnlock()
	return existing
}

func (c *arManager) isInvalidAlloc(allocID string) bool {
	c.invalidAllocsLock.Lock()
	defer c.invalidAllocsLock.Unlock()

	_, ok := c.invalidAllocs[allocID]
	return ok
}

func (c *arManager) markInvalid(alloc *structs.Allocation) {
	if alloc.ClientStatus != structs.AllocClientStatusFailed {
		c.invalidAllocsLock.Lock()
		c.invalidAllocs[alloc.ID] = struct{}{}
		c.invalidAllocsLock.Unlock()
	}
}

// removeAlloc is invoked when we should remove an allocation because it has
// been removed by the server.
func (c *arManager) removeAlloc(allocID string) {
	c.allocLock.Lock()
	defer c.allocLock.Unlock()

	ar, ok := c.allocs[allocID]
	if !ok {
		c.invalidAllocsLock.Lock()
		if _, ok := c.invalidAllocs[allocID]; ok {
			// Removing from invalid allocs map if present
			delete(c.invalidAllocs, allocID)
		} else {
			// Alloc is unknown, log a warning.
			c.logger.Warn("cannot remove nonexistent alloc", "alloc_id", allocID, "error", "alloc not found")
		}
		c.invalidAllocsLock.Unlock()
		return
	}

	// Stop tracking alloc runner as it's been GC'd by the server
	delete(c.allocs, allocID)

	// Ensure the GC has a reference and then collect. Collecting through the GC
	// applies rate limiting
	c.garbageCollector.MarkForCollection(allocID, ar)

	// GC immediately since the server has GC'd it
	go c.garbageCollector.Collect(allocID)
}

// updateAlloc is invoked when we should update an allocation
func (c *arManager) updateAlloc(update *structs.Allocation) {
	ar, err := c.getAllocRunner(update.ID)
	if err != nil {
		c.logger.Warn("cannot update nonexistent alloc", "alloc_id", update.ID)
		return
	}

	// Update local copy of alloc
	if err := c.stateDB.PutAllocation(update); err != nil {
		c.logger.Error("error persisting updated alloc locally", "error", err, "alloc_id", update.ID)
	}

	// Update alloc runner
	ar.Update(update)
}

// addAlloc is invoked when we should add an allocation
func (m *arManager) startAlloc(alloc *structs.Allocation, migrateToken string) error {
	m.allocLock.Lock()
	defer m.allocLock.Unlock()

	// Check if we already have an alloc runner
	if _, ok := m.allocs[alloc.ID]; ok {
		m.logger.Debug("dropping duplicate add allocation request", "alloc_id", alloc.ID)
		m.markInvalid(alloc)
		return nil
	}

	// Initialize local copy of alloc before creating the alloc runner so
	// we can't end up with an alloc runner that does not have an alloc.
	if err := m.stateDB.PutAllocation(alloc); err != nil {
		m.markInvalid(alloc)
		return err
	}

	// Collect any preempted allocations to pass into the previous alloc watcher
	var preemptedAllocs map[string]allocwatcher.AllocRunnerMeta
	if len(alloc.PreemptedAllocations) > 0 {
		preemptedAllocs = make(map[string]allocwatcher.AllocRunnerMeta)
		for _, palloc := range alloc.PreemptedAllocations {
			preemptedAllocs[palloc] = m.allocs[palloc]
		}
	}

	c := m.client

	// Since only the Client has access to other AllocRunners and the RPC
	// client, create the previous allocation watcher here.
	watcherConfig := allocwatcher.Config{
		Alloc:            alloc,
		PreviousRunner:   m.allocs[alloc.PreviousAllocation],
		PreemptedRunners: preemptedAllocs,
		RPC:              c,
		Config:           c.configCopy,
		MigrateToken:     migrateToken,
		Logger:           c.logger,
	}
	prevAllocWatcher, prevAllocMigrator := allocwatcher.NewAllocWatcher(watcherConfig)

	// Copy the config since the node can be swapped out as it is being updated.
	// The long term fix is to pass in the config and node separately and then
	// we don't have to do a copy.
	c.configLock.RLock()
	arConf := &allocrunner.Config{
		Alloc:               alloc,
		Logger:              c.logger,
		ClientConfig:        c.configCopy,
		StateDB:             c.stateDB,
		Consul:              c.consulService,
		Vault:               c.vaultClient,
		StateUpdater:        c,
		DeviceStatsReporter: c,
		PrevAllocWatcher:    prevAllocWatcher,
		PrevAllocMigrator:   prevAllocMigrator,
		DeviceManager:       c.devicemanager,
		DriverManager:       c.drivermanager,
	}
	c.configLock.RUnlock()

	ar, err := allocrunner.NewAllocRunner(arConf)
	if err != nil {
		m.markInvalid(alloc)
		return err
	}

	// Store the alloc runner.
	m.allocs[alloc.ID] = ar

	go ar.Run()
	return nil
}
