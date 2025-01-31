/*
 * Copyright Metaplay. All rights reserved.
 */
package cmd

import (
	"fmt"

	"github.com/metaplay/cli/pkg/auth"
	"github.com/metaplay/cli/pkg/styles"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

type LogoutOpts struct {
}

func init() {
	o := LogoutOpts{}

	cmd := &cobra.Command{
		Use:   "logout",
		Short: "Sign out from Metaplay cloud",
		Long:  `Delete the locally persisted credentials to sign out from Metaplay cloud.`,
		Run:   runCommand(&o),
	}

	authCmd.AddCommand(cmd)
}

func (o *LogoutOpts) Prepare(cmd *cobra.Command, args []string) error {
	if len(args) != 0 {
		return fmt.Errorf("expecting no arguments, got %d", len(args))
	}

	return nil
}

func (o *LogoutOpts) Run(cmd *cobra.Command) error {
	// Check if we're logged in.
	tokenSet, err := auth.LoadTokenSet()
	if err != nil {
		return err
	}

	// If not logged in, just exit.
	if tokenSet == nil {
		log.Info().Msg("")
		log.Info().Msg("Not logged in!")
		return nil
	}

	// Delete the token set.
	err = auth.DeleteTokenSet()
	if err != nil {
		return err
	}

	log.Info().Msg("")
	log.Info().Msg(styles.RenderSuccess("✅ Successfully logged out!"))
	return nil
}
