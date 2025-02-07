/*
 * Copyright Metaplay. All rights reserved.
 */
package tui

// Is the UI library in interactive mode?
var isInteractiveMode = true

func IsInteractiveMode() bool {
	return isInteractiveMode
}

// Set the interactive mode of the UI library.
func SetInteractiveMode(isInteractive bool) {
	isInteractiveMode = isInteractive
}
