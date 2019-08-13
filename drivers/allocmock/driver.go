package allocmock

import (
	"context"
	"fmt"
	"time"

	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/nomad/drivers/shared/eventer"
	"github.com/hashicorp/nomad/nomad/structs"
	"github.com/hashicorp/nomad/plugins/allocdriver"
	"github.com/hashicorp/nomad/plugins/base"
	"github.com/hashicorp/nomad/plugins/drivers"
	"github.com/hashicorp/nomad/plugins/shared/hclspec"
	pstructs "github.com/hashicorp/nomad/plugins/shared/structs"
)

var (
	pluginName = "allocmock"

	// pluginInfo is the response returned for the PluginInfo RPC
	pluginInfo = &base.PluginInfoResponse{
		Type:              base.PluginTypeAllocDriver,
		PluginApiVersions: []string{drivers.ApiVersion010},
		PluginVersion:     "0.1.0",
		Name:              pluginName,
	}

	// configSpec is the hcl specification returned by the ConfigSchema RPC
	configSpec = hclspec.NewObject(map[string]*hclspec.Spec{})

	taskConfigSpec = hclspec.NewObject(map[string]*hclspec.Spec{
		"image": hclspec.NewAttr("image", "string", true),
	})
)

type TaskConfig struct {
	Image string `codec:"image"`
}

type Driver struct {
	// eventer is used to handle multiplexing of TaskEvents calls such that an
	// event can be broadcast to all callers
	eventer *eventer.Eventer

	// ctx is the context for the driver. It is passed to other subsystems to
	// coordinate shutdown
	ctx context.Context

	// signalShutdown is called when the driver is shutting down and cancels the
	// ctx passed to any subsystems
	signalShutdown context.CancelFunc

	// logger will log to the Nomad agent
	logger hclog.Logger

	handler allocdriver.ClientHandler

	taskParser allocdriver.TaskConfigParser
}

var _ allocdriver.AllocDriverPlugin = (*Driver)(nil)

func New(logger hclog.Logger) *Driver {
	ctx, cancel := context.WithCancel(context.Background())
	logger = logger.Named(pluginName)

	return &Driver{
		eventer:        eventer.NewEventer(ctx, logger),
		ctx:            ctx,
		signalShutdown: cancel,
		logger:         logger,
	}
}

func (d *Driver) Initialize(handler allocdriver.ClientHandler) error {
	d.handler = handler
	d.taskParser, _ = handler.TaskConfigParser(taskConfigSpec)
	return nil
}
func (d *Driver) PluginInfo() (*base.PluginInfoResponse, error) {
	return pluginInfo, nil
}

func (d *Driver) ConfigSchema() (*hclspec.Spec, error) {
	return configSpec, nil
}

func (d *Driver) SetConfig(c *base.Config) error {
	return nil
}

func (d *Driver) TaskEvents(context.Context) (<-chan *drivers.TaskEvent, error) {
	return nil, fmt.Errorf("not supported")
}

func (d *Driver) NodeFingerprint() (*allocdriver.NodeFingerprint, error) {
	return &allocdriver.NodeFingerprint{
		CpuShares: 99999,
		MemoryMB:  99999,
		DiskMB:    99999,

		Attributes: map[string]string{
			"simple": "here",
		},
	}, nil
}
func (d *Driver) Fingerprint(context.Context) (<-chan *allocdriver.Fingerprint, error) {
	ch := make(chan *allocdriver.Fingerprint)

	go func() {
		ch <- &allocdriver.Fingerprint{
			Health:            drivers.HealthStateHealthy,
			HealthDescription: drivers.DriverHealthy,
			Attributes: map[string]*pstructs.Attribute{
				"driver.allocmock.sample_attribute": pstructs.NewStringAttribute("1.0"),
			},
		}
	}()
	return ch, nil
}

func (d *Driver) StartAllocation(alloc *structs.Allocation) error {
	d.logger.Info("starting alloc", "alloc_id", alloc.ID)

	for _, t := range alloc.Job.LookupTaskGroup(alloc.TaskGroup).Tasks {
		var c TaskConfig
		env, err := d.taskParser.ParseTask(&c, alloc, t)
		d.logger.Info("launching task",
			"alloc_id", alloc.ID, "task_name", t.Name,
			"config", fmt.Sprintf("%#v", c), "env", env,
			"error", err,
		)
	}

	now := time.Now()
	ts := map[string]*structs.TaskState{}
	for _, task := range alloc.Job.LookupTaskGroup(alloc.TaskGroup).Tasks {
		ts[task.Name] = &structs.TaskState{
			StartedAt: now,
			State:     structs.TaskStateRunning,
		}
	}
	d.handler.UpdateClientStatus(alloc.ID, &allocdriver.AllocState{
		ClientStatus:      structs.AllocClientStatusRunning,
		ClientDescription: "Tasks are running",
		TaskStates:        ts,
	})
	return nil
}
func (d *Driver) StopAllocation(allocID string) error {
	d.logger.Info("stopping alloc", "alloc_id", allocID)
	d.handler.UpdateClientStatus(allocID, &allocdriver.AllocState{
		ClientStatus:      structs.AllocClientStatusComplete,
		ClientDescription: "Tasks were stopped",
	})
	return nil
}
func (d *Driver) UpdateAllocation(alloc *structs.Allocation) error {
	d.logger.Info("updating alloc", "alloc_id", alloc.ID)
	return nil
}

func (d *Driver) SignalAllocation(allocID, taskName, signal string) error {
	return fmt.Errorf("not supported")
}
func (d *Driver) RestartAllocation(allocID, taskName string) error {
	return fmt.Errorf("not supported")
}
func (d *Driver) InspectAllocation(allocID string) (*allocdriver.AllocState, error) {
	return nil, fmt.Errorf("not supported")
}

func (d *Driver) Shutdown() {
	d.signalShutdown()
}
