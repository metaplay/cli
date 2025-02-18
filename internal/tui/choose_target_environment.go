/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */
package tui

import (
	"fmt"

	"github.com/metaplay/cli/pkg/metaproj"
	"github.com/metaplay/cli/pkg/styles"
	"github.com/rs/zerolog/log"
)

func ChooseTargetEnvironmentDialog(environments []metaproj.ProjectEnvironmentConfig) (*metaproj.ProjectEnvironmentConfig, error) {
	if !isInteractiveMode {
		return nil, fmt.Errorf("interactive mode required for project selection")
	}

	// Let the user choose the target pod.
	selected, err := ChooseFromListDialog(
		"Select Target Environment",
		environments,
		func(env *metaproj.ProjectEnvironmentConfig) (string, string) {
			return env.Name, fmt.Sprintf("[%s]", env.HumanID)
		},
	)
	if err != nil {
		return nil, err
	}

	log.Info().Msgf(" %s %s", styles.RenderSuccess("✓"), selected.Name)

	return selected, nil
}
