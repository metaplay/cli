/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

// mariadbCommand returns the argv of a mariadb client that connects to host as user, followed by
// args. It is run without a shell, so a password with shell characters, such as '$' or '&', is
// passed as it is.
func mariadbCommand(host, user, password string, args ...string) []string {
	return append([]string{"mariadb", "--host=" + host, "--user=" + user, "--password=" + password}, args...)
}
