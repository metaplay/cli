/*
 * Copyright Metaplay. All rights reserved.
 */
package cmd

import (
	"fmt"

	"github.com/metaplay/cli/pkg/auth"
	"github.com/spf13/cobra"
)

// Sign in a natural user to Metaplay Auth using the browser.
type LoginOpts struct {
}

func init() {
	o := LoginOpts{}

	cmd := &cobra.Command{
		Use:   "login",
		Short: "Login to your Metaplay account using the browser",
		Run:   runCommand(&o),
	}

	authCmd.AddCommand(cmd)
}

func (o *LoginOpts) Prepare(cmd *cobra.Command, args []string) error {
	if len(args) != 0 {
		return fmt.Errorf("expecting no arguments, got %d", len(args))
	}

	return nil
}

func (o *LoginOpts) Run(cmd *cobra.Command) error {
	err := auth.LoginWithBrowser(cmd.Context())
	if err != nil {
		return err
	}

	return nil
}
