/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package helmutil

import (
	"fmt"
	"time"

	"helm.sh/helm/v4/pkg/action"
	"helm.sh/helm/v4/pkg/kube"
	v1 "helm.sh/helm/v4/pkg/release/v1"
)

// UninstallRelease uninstalls the given Helm release.
func UninstallRelease(actionConfig *action.Configuration, release *v1.Release) error {
	// Create Helm Uninstall action
	uninstall := action.NewUninstall(actionConfig)
	uninstall.Timeout = 5 * time.Minute
	uninstall.WaitStrategy = kube.StatusWatcherStrategy // Wait for resources to be deleted

	// Execute the Uninstall action
	_, err := uninstall.Run(release.Name)
	if err != nil {
		return fmt.Errorf("failed to uninstall Helm release %s: %w", release.Name, err)
	}

	return nil
}
