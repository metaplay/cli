/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package helmutil

import (
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/metaplay/cli/internal/tui"
	"github.com/rs/zerolog/log"
	"gopkg.in/yaml.v3"
	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/chartutil"
	"helm.sh/helm/v3/pkg/cli"
	"helm.sh/helm/v3/pkg/release"
)

// HelmUpgradeOrInstall performs the equivalent of `helm upgrade --install --wait --values <path> ...`
//
// The values are resolved from valuesFiles, defaultValues, and requiredValues.
// Values from the files defined in valuesFiles are applied in order, the later overriding the earlier.
// If a value is not defined in any values-file, the value from defaultValues is used.
//
// The values from requiredValues are used as-is with the highest priority. Any attempt to override
// a value defined in requiredValues with a different value results in an error. Overriding with
// the same value is allowed.
func HelmUpgradeOrInstall(
	output *tui.TaskOutput,
	actionConfig *action.Configuration,
	existingRelease *release.Release,
	namespace, releaseName, chartURL string,
	chartVersion string,
	valuesFiles []string,
	defaultValues map[string]any,
	requiredValues map[string]any,
	timeout time.Duration,
	validateValuesSchema bool,
) (*release.Release, error) {
	// Validate that defaultValues and requiredValues have correct types
	if err := validateHelmValuesTypes(defaultValues, "defaultValues"); err != nil {
		return nil, fmt.Errorf("invalid defaultValues: %w", err)
	}
	if err := validateHelmValuesTypes(requiredValues, "requiredValues"); err != nil {
		return nil, fmt.Errorf("invalid requiredValues: %w", err)
	}

	// Show header at top
	headerLine := fmt.Sprintf("Deploying chart %s as release %s", chartURL, releaseName)
	output.SetHeaderLines([]string{headerLine})

	// Pipe Helm output to task output
	actionConfig.Log = func(format string, args ...any) {
		// Render line and trim any trailing line endings
		line := fmt.Sprintf(format, args...)
		line = strings.TrimRight(line, "\r\n")
		output.AppendLine(line)
	}

	var installCmd *action.Install
	var upgradeCmd *action.Upgrade
	var chartPathOptions *action.ChartPathOptions

	// Determine if install or upgrade based on existence of release:
	// - Use install if no previous Helm release exists
	// - Use upgrade if a previous Helm release exists
	if existingRelease == nil {
		output.AppendLine("No existing release found, install new release")
		installCmd = action.NewInstall(actionConfig)
		installCmd.Version = chartVersion
		installCmd.ReleaseName = releaseName
		installCmd.Namespace = namespace
		installCmd.Wait = true
		installCmd.Timeout = timeout
		installCmd.Devel = true                                 // If version is development, accept it
		installCmd.SkipSchemaValidation = !validateValuesSchema // Disable schema validation for legacy charts
		chartPathOptions = &installCmd.ChartPathOptions
	} else {
		output.AppendLinef("Existing release found (version %s), upgrade existing release", existingRelease.Chart.Metadata.Version)
		upgradeCmd = action.NewUpgrade(actionConfig)
		upgradeCmd.Version = chartVersion
		upgradeCmd.Namespace = namespace
		upgradeCmd.Wait = true
		upgradeCmd.Timeout = timeout
		upgradeCmd.MaxHistory = 10                              // Keep 10 releases max
		upgradeCmd.Devel = true                                 // If version is development, accept it
		upgradeCmd.Atomic = false                               // Don't rollback on failures to not hide errors
		upgradeCmd.CleanupOnFail = true                         // Clean resources on failure
		upgradeCmd.SkipSchemaValidation = !validateValuesSchema // Disable schema validation for legacy charts
		chartPathOptions = &upgradeCmd.ChartPathOptions
	}

	// Load (download) Helm chart
	output.AppendLine("Loading Helm chart...")

	helmClient := cli.New()
	chartPath, err := chartPathOptions.LocateChart(chartURL, helmClient)
	if err != nil {
		return nil, fmt.Errorf("failed to locate Helm chart: %w", err)
	}

	output.AppendLinef("Loading chart from: %s", chartPath)
	loadedChart, err := loader.Load(chartPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load Helm chart: %w", err)
	}

	output.AppendLinef("Chart loaded: %s (version %s)", loadedChart.Name(), loadedChart.Metadata.Version)

	// Construct base values
	baseValues := map[string]any{}
	if defaultValues != nil {
		baseValues = defaultValues
	}

	// Load values from files if any
	filesValueMap := map[string]any{}
	for _, valuesFile := range valuesFiles {
		output.AppendLinef("Loading values from: %s", valuesFile)
		values, err := chartutil.ReadValuesFile(valuesFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read values file: %w", err)
		}

		// Merge with previous values, files processed later override earlier ones
		filesValueMap = mergeValuesMaps(filesValueMap, values.AsMap())
	}

	// Resolve final configurable values map: use defaultValues as base to allow files to override any defaults.
	finalValueMap := mergeValuesMaps(baseValues, filesValueMap)

	// Apply and verify requiredValues are honored
	if requiredValues != nil {
		err = checkRequiredValues(finalValueMap, requiredValues)
		if err != nil {
			return nil, fmt.Errorf("invalid values in helm value files %v: %w", valuesFiles, err)
		}
		finalValueMap = mergeValuesMaps(finalValueMap, requiredValues)
	}

	// Log values as YAML.
	finalValuesYAML, err := yaml.Marshal(finalValueMap)
	if err != nil {
		log.Warn().Msgf("Failed to marshal values as YAML: %+v", finalValueMap)
	} else {
		log.Debug().Msgf("Final Helm values:\n%s", finalValuesYAML)
	}

	// Run install or upgrade install
	output.AppendLine("Starting Helm deployment...")
	if installCmd != nil {
		output.AppendLine("Installing new release...")
		release, err := installCmd.Run(loadedChart, finalValueMap)
		if err != nil {
			return nil, fmt.Errorf("failed to install the Helm chart: %w", err)
		}
		return release, nil
	} else {
		output.AppendLine("Upgrading existing release...")
		release, err := upgradeCmd.Run(releaseName, loadedChart, finalValueMap)
		if err != nil {
			return nil, fmt.Errorf("failed to upgrade an existing Helm release: %w", err)
		}
		return release, nil
	}
}

// Combine two Helm values maps into one. On conflicts, the fields in 'override' win
// over 'base'. Maps are recursively merged. Sequences are replaced.
func mergeValuesMaps(base, override map[string]any) map[string]any {
	// Clone base.
	combined := make(map[string]any, len(base))
	for k, v := range base {
		combined[k] = v
	}

	// Merge all keys from override (recursively merge maps).
	for k, v := range override {
		if v, ok := v.(map[string]any); ok {
			if bv, ok := combined[k]; ok {
				if bv, ok := bv.(map[string]any); ok {
					combined[k] = mergeValuesMaps(bv, v)
					continue
				}
			}
		}
		combined[k] = v
	}
	return combined
}

// Check all values does not have conflicting declarations to required.
// Specifically, any value in required must either have the same value in values
// or be not present in values.
func checkRequiredValues(values, required map[string]any) error {
	return doCheckRequiredValues(values, required, "")
}

func doCheckRequiredValues(inspected, required map[string]any, path string) error {
	for k, requiredV := range required {
		// Check if not set in values
		inspectedV, ok := inspected[k]
		if !ok {
			// Not in values, not conflicting. Ok
			continue
		}

		// Recursively check mappings
		inspectedVMap, isInspectedVMap := inspectedV.(map[string]any)
		requiredVMap, isRequiredVMap := requiredV.(map[string]any)
		if isInspectedVMap && isRequiredVMap {
			err := doCheckRequiredValues(inspectedVMap, requiredVMap, path+k+".")
			if err != nil {
				return err
			}
			continue
		} else if isInspectedVMap {
			return fmt.Errorf("structural error, %q must be a scalar, not a mapping", path)
		} else if isRequiredVMap {
			return fmt.Errorf("structural error, %q must be a mapping, not a scalar", path)
		}

		// Check scalars are equal
		if requiredV != inspectedV {
			return fmt.Errorf("scalar %q must not be set or must be %q, but got %q", path+k, requiredV, inspectedV)
		}
	}
	return nil
}

// validateHelmValuesTypes recursively validates that all arrays are []any and all maps are map[string]any.
// The underlying library that handles Helm values validation within the Helm library requires the values
// to be exactly of these types. Properly-typed arrays and maps cause validation errors.
func validateHelmValuesTypes(values map[string]any, path string) error {
	for key, value := range values {
		currentPath := path + "." + key
		if path == "" {
			currentPath = key
		}

		if err := validateValueType(value, currentPath); err != nil {
			return err
		}
	}
	return nil
}

// validateValueType validates a single value recursively. See validateHelmValuesTypes for details.
func validateValueType(value any, path string) error {
	if value == nil {
		return nil
	}

	v := reflect.ValueOf(value)
	t := v.Type()

	switch v.Kind() {
	case reflect.Slice:
		// Check if it's []any
		if t != reflect.TypeOf([]any{}) {
			return fmt.Errorf("invalid array type at %s: expected []any, got %s", path, t)
		}
		// Recursively validate slice elements
		for i := 0; i < v.Len(); i++ {
			elementPath := fmt.Sprintf("%s[%d]", path, i)
			if err := validateValueType(v.Index(i).Interface(), elementPath); err != nil {
				return err
			}
		}
	case reflect.Map:
		// Check if it's map[string]any
		if t != reflect.TypeOf(map[string]any{}) {
			return fmt.Errorf("invalid map type at %s: expected map[string]any, got %s", path, t)
		}
		// Recursively validate map values
		for _, mapKey := range v.MapKeys() {
			keyStr := mapKey.String()
			mapValue := v.MapIndex(mapKey).Interface()
			mapPath := path + "." + keyStr
			if err := validateValueType(mapValue, mapPath); err != nil {
				return err
			}
		}
	}

	return nil
}
