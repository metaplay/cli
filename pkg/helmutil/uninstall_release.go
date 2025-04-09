/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */
package helmutil

import (
	"fmt"
	"time"

	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/release"
)

// UninstallRelease uninstalls the given Helm release.
func UninstallRelease(actionConfig *action.Configuration, release *release.Release) error {
	// Create Helm Uninstall action
	uninstall := action.NewUninstall(actionConfig)
	uninstall.Wait = true
	uninstall.Timeout = 5 * time.Minute

	// Execute the Uninstall action
	_, err := uninstall.Run(release.Name)
	if err != nil {
		return fmt.Errorf("failed to uninstall Helm release %s: %w", release.Name, err)
	}

	return nil
}
