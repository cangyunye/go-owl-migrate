package cmd

import (
	"github.com/spf13/cobra"
)

// genInsertCmd is a hidden alias for "export insert". It remains registered for
// backward compatibility with existing scripts.
func genInsertCmd() *cobra.Command {
	exportInsert := exportInsertCmd()
	exportInsert.Use = "gen-insert"
	exportInsert.Hidden = true
	exportInsert.Example = ""
	return exportInsert
}
