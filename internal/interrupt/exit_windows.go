/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package interrupt

import "os"

// exitOnSignal exits with ExitCode. Go's default handler would exit with STATUS_CONTROL_C_EXIT
// (3221225786), which scripts do not recognize as an interrupt.
func exitOnSignal(os.Signal) {
	os.Exit(ExitCode)
}
