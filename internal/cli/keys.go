package cli

import (
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/fakihariefnoto/omssh/internal/actions"
)

func newKeysCmd(opts *Options, stdout io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "keys",
		Short: "Show the effective keymap",
		Long: "Show every action, the keys bound to it, and any binding that was\n" +
			"dropped because this terminal cannot deliver it.",
		Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			cfg, _, err := loadConfig(opts)
			if err != nil {
				return err
			}
			reg, keys, err := buildKeymap(cfg)
			if err != nil {
				return err
			}
			fmt.Fprintln(stdout, "bindings are one map: plain letters are commands, digits switch tabs")
			fmt.Fprintln(stdout)
			writer := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
			lastCategory := actions.Category("")
			for _, a := range reg.Palette() {
				if a.Category != lastCategory {
					fmt.Fprintf(writer, "\n%s\n", a.Category)
					lastCategory = a.Category
				}
				bound := ""
				for i, k := range keys.Keys(a.ID) {
					if i > 0 {
						bound += ", "
					}
					bound += k
				}
				if bound == "" {
					bound = "—"
				}
				fmt.Fprintf(writer, "  %s\t%s\t%s\n", bound, a.ID, a.Label)
			}
			if err := writer.Flush(); err != nil {
				return err
			}
			if dropped := keys.Dropped(); len(dropped) > 0 {
				fmt.Fprintln(stdout, "\nunavailable in this terminal:")
				for _, d := range dropped {
					fmt.Fprintf(stdout, "  %s (%s): %s\n", d.Key, d.Action, d.Reason)
				}
			}
			return nil
		},
	}
}
