package cmd

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/metaplay/cli/pkg/auth"
	"github.com/metaplay/cli/pkg/envapi"
	"github.com/metaplay/cli/pkg/portalapi"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

// environmentCmd represents the environment command
var environmentCmd = &cobra.Command{
	Use:     "environment",
	Aliases: []string{"env"},
	Short:   "Commands for managing Metaplay cloud environments",
}

var flagEnvironmentID string
var flagSlugs string
var flagStackApiBaseURL string

func init() {
	rootCmd.AddCommand(environmentCmd)

	environmentCmd.PersistentFlags().StringVarP(&flagEnvironmentID, "environment", "e", "", "Identity of the environment, eg, 'tiny-squids'")
	environmentCmd.PersistentFlags().StringVarP(&flagSlugs, "slugs", "s", "", "Slugs of the environment, eg, 'metaplay-idler-test'")
	environmentCmd.PersistentFlags().StringVar(&flagStackApiBaseURL, "stack-api", "", "Base URL to the StackAPI hosting the environment, eg, 'https://infra.p1.metaplay.io/stackapi")
}

type TargetEnvSelector struct {
	HumanId          string // Environment human ID, eg, 'tiny-squids'
	OrganizationSlug string // Slug for the organization, eg, 'metaplay'
	ProjectSlug      string // Slug for the project, eg, 'idler'
	EnvironmentSlug  string // Slug for the project, eg, 'develop'
}

func (selector *TargetEnvSelector) String() string {
	if selector.HumanId != "" {
		return "id:" + selector.HumanId
	} else {
		return fmt.Sprintf("slugs:%s-%s-%s", selector.OrganizationSlug, selector.ProjectSlug, selector.EnvironmentSlug)
	}
}

// isValidEnvironmentId checks whether the given environment ID is valid.
// The valid format must be dash-separated parts with alphanumeric characters
// only. There can be either 2 or 3 parts. Eg, 'tiny-squids' or 'idler-develop-12345'.
func isValidEnvironmentId(id string) bool {
	// Split the string by dashes
	parts := strings.Split(id, "-")

	// The ID should have 2 or 3 parts
	if len(parts) < 2 || len(parts) > 3 {
		return false
	}

	// Validate that each part contains only alphanumeric characters
	validPart := regexp.MustCompile(`^[a-zA-Z0-9]+$`)
	for _, part := range parts {
		if !validPart.MatchString(part) {
			return false
		}
	}

	// If all checks pass, the ID is valid
	return true
}

// parseEnvironmentSlugs parses the given environment slug triplet.
// The valid format must be dash-separated parts with alphanumeric characters
// only. There must be exactly 3 parts. Eg, 'metaplay-idler-develop'.
func parseEnvironmentSlugs(slugs string) ([]string, error) {
	// Split the string by dashes
	parts := strings.Split(slugs, "-")

	// The slugs must have exactly 3 parts
	if len(parts) != 3 {
		return nil, errors.New("the slugs must consist of exactly 3 dash-separated parts")
	}

	// Validate that each part contains only alphanumeric characters
	validPart := regexp.MustCompile(`^[a-zA-Z0-9]+$`)
	for _, part := range parts {
		if !validPart.MatchString(part) {
			return nil, errors.New("the slugs can only contain alphanumeric characters")
		}
	}

	// If all checks pass, the ID is valid
	return parts, nil
}

func getTargetEnvironmentSelector() *TargetEnvSelector {
	// Check that only one of environment and slugs are provided.
	if flagEnvironmentID != "" && flagSlugs != "" {
		log.Error().Msg("ERROR: Only one of --environment and --slugs must be used to specify target environment")
		os.Exit(2)
	} else if flagEnvironmentID == "" && flagSlugs == "" {
		log.Error().Msg("ERROR: Target environment must be specified using --environment=<id> or --slugs=<slugs>")
		os.Exit(2)
	}

	// Handle humanID vs slugs.
	if flagEnvironmentID != "" {
		// Validate the environment ID.
		if !isValidEnvironmentId(flagEnvironmentID) {
			log.Error().Msgf("Invalid environment ID '%s'. Environment IDs must be dash-separated strings of alphanumeric characters, eg, 'tiny-squids' or 'idler-develop-feature4'", flagEnvironmentID)
			os.Exit(2)
		}

		// Return selector with either environment id or slugs.
		return &TargetEnvSelector{
			HumanId: flagEnvironmentID,
		}
	} else {
		// Parse slugs into individual parts.
		parts, err := parseEnvironmentSlugs(flagSlugs)
		if err != nil {
			log.Error().Msgf("Invalid format for slugs: %v. The slugs must be of the format '<organization>-<project>-<environment>'.", flagSlugs)
			os.Exit(2)
		}

		// Return selector with either environment id or slugs.
		return &TargetEnvSelector{
			OrganizationSlug: parts[0],
			ProjectSlug:      parts[1],
			EnvironmentSlug:  parts[2],
		}
	}
}

func resolveTargetEnvironment(tokenSet *auth.TokenSet) (*envapi.TargetEnvironment, error) {
	// Resolve target environment.
	selector := getTargetEnvironmentSelector()
	log.Debug().Msgf("Resolving information about environment %s...", selector)

	// If both StackAPI base URL and environment ID are provided, use them directly.
	if flagStackApiBaseURL != "" && selector.HumanId != "" {
		return envapi.NewTargetEnvironment(tokenSet, flagStackApiBaseURL, selector.HumanId), nil
	}

	// Resolve environment information from the portal.
	var portalEnvInfo *portalapi.PortalEnvironmentInfo
	var err error
	if selector.HumanId != "" {
		portalEnvInfo, err = portalapi.FetchInfoByHumanID(tokenSet, selector.HumanId)
	} else {
		portalEnvInfo, err = portalapi.FetchInfoBySlugs(tokenSet, selector.OrganizationSlug, selector.ProjectSlug, selector.EnvironmentSlug)
	}
	if err != nil {
		return nil, err
	}
	log.Debug().Msgf("Portal information: %+v", portalEnvInfo)

	// Environment must be provisioned on a stack.
	if portalEnvInfo.StackDomain == nil {
		return nil, fmt.Errorf("environment is not provisioned onto any stack! Contact Metaplay for more help.")
	}

	stackApiBaseURL := fmt.Sprintf("https://infra.%s/stackapi", *portalEnvInfo.StackDomain)
	return envapi.NewTargetEnvironment(tokenSet, stackApiBaseURL, portalEnvInfo.HumanID), nil
}
