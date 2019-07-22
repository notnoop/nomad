package launcher

import (
	"context"
	"fmt"
	"os"
	"time"

	hclog "github.com/hashicorp/go-hclog"
	clientconfig "github.com/hashicorp/nomad/client/config"
	"github.com/hashicorp/nomad/client/fingerprint"
	"github.com/hashicorp/nomad/command/agent"
	"github.com/hashicorp/nomad/helper/pluginutils/catalog"
	"github.com/hashicorp/nomad/helper/pluginutils/loader"
	"github.com/hashicorp/nomad/nomad/structs"
	"github.com/hashicorp/nomad/plugins/allocdriver"
	"github.com/hashicorp/nomad/plugins/base"
	"github.com/hashicorp/nomad/plugins/drivers"
	"github.com/hashicorp/nomad/plugins/shared/hclspec"
	"github.com/hashicorp/nomad/version"
	"github.com/mitchellh/cli"
)

func Serve(pluginFn func(hclog.Logger) interface{}) {
	args := make([]string, len(os.Args))
	copy(args, os.Args)
	args[0] = "agent"
	os.Exit(RunMain(args, pluginFn))
}

func RunMain(args []string, pluginFn func(hclog.Logger) interface{}) int {

	// The Nomad agent never outputs color
	agentUi := &cli.BasicUi{
		Reader:      os.Stdin,
		Writer:      os.Stdout,
		ErrorWriter: os.Stderr,
	}

	commands := commands(agentUi, pluginFn)
	cli := &cli.CLI{
		Name:                       os.Args[0],
		Version:                    version.GetVersion().FullVersionNumber(true),
		Args:                       args,
		Commands:                   commands,
		Autocomplete:               true,
		AutocompleteNoDefaultFlags: true,
		HelpWriter:                 os.Stdout,
	}

	exitCode, err := cli.Run()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error executing CLI: %s\n", err.Error())
		return 1
	}

	return exitCode

}

func commands(agentUi cli.Ui, pluginFn func(hclog.Logger) interface{}) map[string]cli.CommandFactory {
	all := map[string]cli.CommandFactory{
		"agent": func() (cli.Command, error) {
			return &cmd{
				pluginFn: pluginFn,
				Command: agent.Command{
					Version:    version.GetVersion(),
					Ui:         agentUi,
					ShutdownCh: make(chan struct{}),
				}}, nil
		},
	}

	return all
}

type cmd struct {
	agent.Command
	pluginFn func(hclog.Logger) interface{}
}

func (c *cmd) registerPlugin(pluginInfo *base.PluginInfoResponse) {
	// register as an alloc driver

	pluginID := loader.PluginID{
		Name:       pluginInfo.Name,
		PluginType: pluginInfo.Type,
	}
	pluginConfig := &loader.InternalPluginConfig{
		Config:  map[string]interface{}{},
		Factory: c.pluginFn,
	}
	catalog.Register(pluginID, pluginConfig)

	// register as a plain driver
	driverPluginID := loader.PluginID{
		Name: pluginInfo.Name,

		// override plugin type to be a plain driver to ease fingerprinting
		PluginType: base.PluginTypeDriver,
	}
	driverPluginConfig := &loader.InternalPluginConfig{
		Config: map[string]interface{}{},
		Factory: func(logger hclog.Logger) interface{} {
			p, ok := c.pluginFn(logger).(allocdriver.AllocDriverPlugin)
			if !ok {
				panic("plugin is not an AllocDriverPlugin")
			}

			return &allocDriverWrapper{d: p}
		},
	}

	catalog.Register(driverPluginID, driverPluginConfig)
}

func (c *cmd) Run(args []string) int {
	return c.Command.RunWithCustomConfig(args, func(config *agent.Config, logger hclog.Logger) {
		if config.Client != nil {
			config.Client.Virtual = true
			config.Client.Options["fingerprint.whitelist"] = "consul,nomad,vault"
		}

		if config.ClientConfig == nil {
			config.ClientConfig = &clientconfig.Config{}
		}

		plugin := c.pluginFn(logger).(allocdriver.AllocDriverPlugin)

		plugInfo, err := plugin.PluginInfo()
		if err != nil {
			panic("unexpected error getting plugin info")
		}

		c.registerPlugin(plugInfo)
		config.Client.Options["driver.whitelist"] = plugInfo.Name

		config.ClientConfig.CustomFingerprinters = map[string]func(hclog.Logger) interface{}{
			"custom": func(hclog.Logger) interface{} { return &fingerprinter{allocDriver: plugin} },
		}
	})
}

type fingerprinter struct {
	allocDriver allocdriver.AllocDriverPlugin
}

func (f *fingerprinter) Fingerprint(req *fingerprint.FingerprintRequest, resp *fingerprint.FingerprintResponse) error {
	r, err := f.allocDriver.NodeFingerprint()
	if err != nil {
		return err
	}

	resp.Detected = true

	resp.NodeResources = &structs.NodeResources{}
	resp.NodeResources.Cpu.CpuShares = r.CpuShares
	resp.NodeResources.Memory.MemoryMB = r.MemoryMB
	resp.NodeResources.Disk.DiskMB = r.DiskMB

	resp.Resources = &structs.Resources{
		CPU:      int(r.CpuShares),
		MemoryMB: int(r.MemoryMB),
		DiskMB:   int(r.DiskMB),
	}

	resp.Attributes = r.Attributes
	resp.Links = r.Links

	return nil
}

func (f *fingerprinter) Periodic() (bool, time.Duration) {
	return false, 0
}

type allocDriverWrapper struct {
	drivers.DriverPlugin

	d allocdriver.AllocDriverPlugin
}

func (a *allocDriverWrapper) PluginInfo() (*base.PluginInfoResponse, error) {
	r, err := a.d.PluginInfo()
	if err != nil {
		return nil, err
	}
	r.Type = base.PluginTypeDriver
	return r, nil
}

func (a *allocDriverWrapper) ConfigSchema() (*hclspec.Spec, error) {
	return a.d.ConfigSchema()
}

func (a *allocDriverWrapper) SetConfig(c *base.Config) error {
	return a.d.SetConfig(c)
}

func (a *allocDriverWrapper) Fingerprint(ctx context.Context) (<-chan *drivers.Fingerprint, error) {
	return a.d.Fingerprint(ctx)
}

func (a *allocDriverWrapper) TaskEvents(ctx context.Context) (<-chan *drivers.TaskEvent, error) {
	return a.d.TaskEvents(ctx)
}

var _ base.BasePlugin = (*allocDriverWrapper)(nil)
