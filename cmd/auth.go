/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"github.com/spf13/cobra"
)

// authCmd represents the auth command
var authCmd = &cobra.Command{
	Use:   "auth",
	Short: "Authenticate to Metaplay Cloud",
	Long: `Commands related to authenticating with Metaplay Cloud.
Supports sign in via a browser for human users and using a secret for machine users.

A human user signs in with 'auth login'. When the browser is not on the machine running
the CLI, such as over SSH, in a container or when a coding agent runs the CLI, use
'auth login --no-browser' instead. CI and other unattended use signs in with
'auth machine-login'.`,
}

func init() {
	rootCmd.AddCommand(authCmd)
}
