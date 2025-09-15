// Copyright (c) Omlox Client Go Contributors
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
	"github.com/wavecomtech/omlox-client-go"
	"github.com/wavecomtech/omlox-client-go/internal/cli"
	"github.com/wavecomtech/omlox-client-go/internal/cli/resource"
)

const updateTrackableHelp = `
This command updates trackables in the Omlox Hub.
`

func newUpdateTrackablesCmd(settings cli.EnvSettings, out io.Writer) *cobra.Command {
	var files []string

	cmd := &cobra.Command{
		Use:   "trackables",
		Short: "Update trackables in the Hub",
		Long:  updateTrackableHelp,
		Args:  cobra.ExactArgs(0),
		RunE: func(cmd *cobra.Command, args []string) error {
			var in []io.Reader

			if len(files) > 0 {
				for _, name := range files {
					f, err := os.OpenFile(name, os.O_RDONLY, 0444)
					if err != nil {
						return err
					}
					defer f.Close()

					in = append(in, f)
				}
			} else {
				in = append(in, cmd.InOrStdin())
			}

			loader := resource.Loader[omlox.Trackable]{
				Resources: make([]omlox.Trackable, 0),
			}
			for _, r := range in {
				if err := loader.LoadJSON(r); err != nil {
					return err
				}
			}

			c, err := newOmloxClient(&settings)
			if err != nil {
				return err
			}

			for _, t := range loader.Resources {
				err := c.Trackables.Update(context.Background(), t, t.ID)
				if err != nil {
					return err
				}

				fmt.Fprintf(out, "updated: %v %v\n", t.ID, t.Name)
			}

			return nil
		},
	}

	f := cmd.Flags()
	f.StringArrayVarP(&files, "file", "f", []string{}, "The files that contain the trackables to update")

	return cmd
}
