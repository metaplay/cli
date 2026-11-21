/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import "github.com/metaplay/cli/pkg/kubeutil"

// mariadbConnectionArgs returns the shell-quoted arguments that connect a mariadb client to host
// as user. Quoting keeps a password with shell characters, such as '$' or '&', as it is: unquoted,
// the shell would change it or split the command.
func mariadbConnectionArgs(host, user, password string) string {
	return "--host=" + kubeutil.ShellQuote(host) + " --user=" + kubeutil.ShellQuote(user) + " --password=" + kubeutil.ShellQuote(password)
}
