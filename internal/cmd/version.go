package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/cangyunye/go-owl-migrate/internal/dbconn"
	"github.com/cangyunye/go-owl-migrate/internal/registry"
)

// versionCmd reports the build identity plus what this binary can actually
// reach: the database/sql drivers linked in and the dialects compiled in.
// Both depend on the build tag set (see the Makefile / build.yml flavors), so
// this answers "can this binary talk to PanWeiDB / OceanBase / SQLite?" without
// attempting a connection or reverse-engineering the binary.
func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Show version, linked database drivers and compiled dialects",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			w := cmd.OutOrStdout()
			fmt.Fprintf(w, "owl-migrate %s\n", version)
			fmt.Fprintf(w, "  commit:       %s\n", commitID)
			fmt.Fprintf(w, "  built:        %s\n", buildTime)
			fmt.Fprintf(w, "  drivers:      %s\n", strings.Join(linkedDrivers(), ", "))
			fmt.Fprintf(w, "  dialects:     %s\n", strings.Join(registry.Names(), ", "))
			if missing := missingDrivers(); len(missing) > 0 {
				fmt.Fprintf(w, "  not linked:   %s\n", strings.Join(missing, ", "))
			}
			return nil
		},
	}
}

// linkedDrivers returns the selectable drivers this binary links, in report
// order.
func linkedDrivers() []string {
	var out []string
	for _, d := range dbconn.DriverReport() {
		if d.Linked {
			out = append(out, d.Driver)
		}
	}
	return out
}

// missingDrivers formats the selectable drivers this binary was built without,
// each with the build tag that would add it.
func missingDrivers() []string {
	var out []string
	for _, d := range dbconn.DriverReport() {
		if d.Linked {
			continue
		}
		if d.Tag != "" {
			out = append(out, fmt.Sprintf("%s (-tags %s)", d.Driver, d.Tag))
			continue
		}
		out = append(out, d.Driver)
	}
	return out
}
