package main

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/cluion/bridra/backend/internal/releaseinfo"
)

var errUsage = errors.New("invalid Bridra CLI usage")

type command interface {
	name() string
	summary() string
	usage() string
	run(arguments []string, stdout, stderr io.Writer) error
}

type application struct {
	commands     map[string]command
	commandOrder []string
}

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(arguments []string, stdout, stderr io.Writer) error {
	return newApplication(defaultDoctorSystem()).run(arguments, stdout, stderr)
}

func newApplication(system doctorSystem) *application {
	registered := []command{
		newVersionCommand(),
		createCommand{system: defaultCreateSystem()},
		newUpgradeCommand(),
		newReleaseCommand(),
		makeCommand{},
		devCommand{system: defaultDevSystem()},
		buildCommand{system: defaultBuildSystem()},
		generateCommand{},
		doctorCommand{system: system},
	}
	commands := make(map[string]command, len(registered))
	order := make([]string, 0, len(registered))
	for _, item := range registered {
		commands[item.name()] = item
		order = append(order, item.name())
	}
	return &application{commands: commands, commandOrder: order}
}

func (app *application) run(arguments []string, stdout, stderr io.Writer) error {
	if len(arguments) == 0 {
		app.writeHelp(stdout)
		return nil
	}

	switch arguments[0] {
	case "-h", "--help":
		app.writeHelp(stdout)
		return nil
	case "help":
		return app.runHelp(arguments[1:], stdout)
	}

	item, exists := app.commands[arguments[0]]
	if !exists {
		return fmt.Errorf(
			"%w: unknown command %q; run 'bridra help' for available commands",
			errUsage,
			arguments[0],
		)
	}
	return item.run(arguments[1:], stdout, stderr)
}

func (app *application) runHelp(arguments []string, stdout io.Writer) error {
	if len(arguments) == 0 {
		app.writeHelp(stdout)
		return nil
	}
	if len(arguments) != 1 {
		return fmt.Errorf("%w: usage: bridra help [command]", errUsage)
	}
	item, exists := app.commands[arguments[0]]
	if !exists {
		return fmt.Errorf("%w: unknown command %q", errUsage, arguments[0])
	}
	fmt.Fprintln(stdout, item.usage())
	return nil
}

func (app *application) writeHelp(output io.Writer) {
	fmt.Fprintf(output, "Bridra %s\n\n", releaseinfo.Version)
	fmt.Fprintln(output, "Usage:")
	fmt.Fprintln(output, "  bridra <command> [options]")
	fmt.Fprintln(output)
	fmt.Fprintln(output, "Commands:")
	for _, name := range app.commandOrder {
		item := app.commands[name]
		fmt.Fprintf(output, "  %-10s %s\n", item.name(), item.summary())
	}
	fmt.Fprintln(output)
	fmt.Fprintln(output, "Run 'bridra help <command>' for command details.")
}
