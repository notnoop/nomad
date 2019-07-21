package allocdriver

import (
	"fmt"
	"os"
	"time"

	hclog "github.com/hashicorp/go-hclog"
	clientconfig "github.com/hashicorp/nomad/client/config"
	"github.com/hashicorp/nomad/client/fingerprint"
	"github.com/hashicorp/nomad/command"
	"github.com/hashicorp/nomad/command/agent"
	"github.com/hashicorp/nomad/nomad/structs"
	"github.com/hashicorp/nomad/version"
	"github.com/mattn/go-colorable"
	"github.com/mitchellh/cli"
	"golang.org/x/crypto/ssh/terminal"
)

func Serve(pluginFn func(hclog.Logger) interface{}) {
	args := make([]string, len(os.Args))
	copy(args, os.Args)
	args[0] = "agent"
	os.Exit(RunMain(args, pluginFn))
}

func RunMain(args []string, pluginFn func(hclog.Logger) interface{}) int {

	// Create the meta object
	metaPtr := new(command.Meta)

	// Don't use color if disabled
	color := true
	if os.Getenv(command.EnvNomadCLINoColor) != "" {
		color = false
	}

	isTerminal := terminal.IsTerminal(int(os.Stdout.Fd()))
	metaPtr.Ui = &cli.BasicUi{
		Reader:      os.Stdin,
		Writer:      colorable.NewColorableStdout(),
		ErrorWriter: colorable.NewColorableStderr(),
	}

	// The Nomad agent never outputs color
	agentUi := &cli.BasicUi{
		Reader:      os.Stdin,
		Writer:      os.Stdout,
		ErrorWriter: os.Stderr,
	}

	// Only use colored UI if stdout is a tty, and not disabled
	if isTerminal && color {
		metaPtr.Ui = &cli.ColoredUi{
			ErrorColor: cli.UiColorRed,
			WarnColor:  cli.UiColorYellow,
			InfoColor:  cli.UiColorGreen,
			Ui:         metaPtr.Ui,
		}
	}

	commands := commands(metaPtr, agentUi, pluginFn)
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

func commands(metaPtr *command.Meta, agentUi cli.Ui, pluginFn func(hclog.Logger) interface{}) map[string]cli.CommandFactory {
	if metaPtr == nil {
		metaPtr = new(command.Meta)
	}

	meta := *metaPtr
	if meta.Ui == nil {
		meta.Ui = &cli.BasicUi{
			Reader:      os.Stdin,
			Writer:      colorable.NewColorableStdout(),
			ErrorWriter: colorable.NewColorableStderr(),
		}
	}

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

func (c *cmd) Run(args []string) int {
	return c.Command.RunWithCustomConfig(args, func(config *agent.Config, logger hclog.Logger) {
		if config.Client != nil {
			config.Client.Virtual = true
			config.Client.Options["fingerprint.whitelist"] = "consul,nomad,vault"
		}

		if config.ClientConfig == nil {
			config.ClientConfig = &clientconfig.Config{}
		}

		plugin := c.pluginFn(logger).(AllocDriverPlugin)
		config.ClientConfig.CustomFingerprinters = map[string]func(hclog.Logger) interface{}{
			"custom": func(hclog.Logger) interface{} { return &fingerprinter{allocDriver: plugin} },
		}
	})
}

type fingerprinter struct {
	allocDriver AllocDriverPlugin
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
