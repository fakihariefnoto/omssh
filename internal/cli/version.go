package cli

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/fakihariefnoto/omssh/internal/version"
)

func newVersionCmd(stdout io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print build information",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			fmt.Fprintln(stdout, version.String())
			return nil
		},
	}
}
