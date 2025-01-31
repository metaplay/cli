/*
 * Copyright Metaplay. All rights reserved.
 */
package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/metaplay/cli/internal/tui"
	"github.com/metaplay/cli/pkg/envapi"
	"github.com/metaplay/cli/pkg/helmutil"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// \todo Refactor implementation to use Kubernetes APIs directly, not use 'kubectl'.
// \todo Implement cleaning up ephemeral containers from the target pod.

// Start a Kubernetes ephemeral debug container within the specified pod.
type debugRunShellOpts struct {
	argEnvironment string
	argPodName     string
}

func init() {
	o := debugRunShellOpts{}

	cmd := &cobra.Command{
		Use:   "run-shell ENVIRONMENT [POD] [flags]",
		Short: "[experimental] Start a debug container targeting the specified pod",
		Long: trimIndent(`
			Start a debug container targeting a game server pod in the specified environment.
			This command creates a Kubernetes ephemeral debug container that attaches to an existing
			game server pod, allowing you to inspect and troubleshoot the running server.

			WARNING: This is an experimental feature and interface is likely to change. For now,
			it also requires 'kubectl' to be locally installed to work.

			If multiple game server pods are running in the environment, you must specify which pod
			to debug by providing its name as the second argument. If only one pod is running,
			the pod name is optional.

			The debug container uses the metaplay/diagnostics:latest image which contains various
			debugging and diagnostic tools. The container is attached to the shard-server container
			within the pod, giving you direct access to the game server process.

			Arguments:
			- ENVIRONMENT is the target environment in the current project.
			- POD (optional) is name of the pod to target. Must be given when multiple pods exist.
		`),
		Example: trimIndent(`
			# Start a debug container in the 'tough-falcons' environment (when only one pod is running).
			metaplay debug run-shell tough-falcons

			# Start a debug container pod named 'service-0' in the environment 'tough-falcons'.
			metaplay debug run-shell tough-falcons service-0
		`),
		Run: runCommand(&o),
	}

	debugCmd.AddCommand(cmd)
}

func (o *debugRunShellOpts) Prepare(cmd *cobra.Command, args []string) error {
	if len(args) < 1 || len(args) > 2 {
		return fmt.Errorf("exactly one or two arguments must be provided, got %d", len(args))
	}

	o.argEnvironment = args[0]

	if len(args) >= 2 {
		o.argPodName = args[1]
	}

	return nil
}

func (o *debugRunShellOpts) Run(cmd *cobra.Command) error {
	// Ensure the user is logged in
	tokenSet, err := tui.RequireLoggedIn(cmd.Context())
	if err != nil {
		return err
	}

	// Resolve environment.
	envConfig, err := resolveEnvironment(tokenSet, o.argEnvironment)
	if err != nil {
		return err
	}

	// Create environment helper.
	targetEnv := envapi.NewTargetEnvironment(tokenSet, envConfig.StackDomain, envConfig.HumanID)

	// Get kubeconfig to access the environment.
	kubeconfigPayload, err := targetEnv.GetKubeConfigWithEmbeddedCredentials()
	log.Debug().Msgf("Resolved kubeconfig to access environment")

	// Configure Helm.
	actionConfig, err := helmutil.NewActionConfig(*kubeconfigPayload, envConfig.getKubernetesNamespace())
	if err != nil {
		log.Error().Msgf("Failed to initialize Helm config: %v", err)
		os.Exit(1)
	}

	// Resolve all deployed game server Helm releases.
	helmReleases, err := helmutil.HelmListReleases(actionConfig, metaplayGameServerChartName)
	if len(helmReleases) == 0 {
		log.Error().Msgf("No game server deployment found")
		os.Exit(0)
	}

	// Create a clientset to access Kubernetes.
	clientset, err := targetEnv.NewKubernetesClientSet()
	if err != nil {
		log.Error().Msgf("Failed to initialize Kubernetes client: %v", err)
		os.Exit(1)
	}

	// Get running game server pods.
	kubernetesNamespace := envConfig.getKubernetesNamespace()
	log.Debug().Msgf("Get running game server pods")
	pods, err := clientset.CoreV1().Pods(kubernetesNamespace).List(cmd.Context(), metav1.ListOptions{
		LabelSelector: metaplayGameServerPodLabelSelector,
	})
	if err != nil {
		log.Error().Msgf("Failed to list pods: %v", err)
		os.Exit(1)
	}

	// Fetch all gameserver pods.
	gameServerPods := pods.Items
	if len(gameServerPods) == 0 {
		log.Error().Msgf("No game server pods found in namespace %s", kubernetesNamespace)
		os.Exit(1)
	}

	// Resolve target pod name
	targetPodName := o.argPodName
	if targetPodName == "" {
		if len(gameServerPods) == 1 {
			targetPodName = gameServerPods[0].Name
		} else {
			var podNames []string
			for _, pod := range gameServerPods {
				podNames = append(podNames, pod.Name)
			}
			log.Warn().Msgf("Multiple game server pods running: %v", strings.Join(podNames, ", "))
			log.Error().Msgf("Specify which pod you want to debug with the argument POD")
			os.Exit(2)
		}
	}

	// Write kubeconfig to a temporary file
	tmpKubeconfigPath := filepath.Join(os.TempDir(), fmt.Sprintf("temp-kubeconfig-%d", time.Now().Unix()))
	err = os.WriteFile(tmpKubeconfigPath, []byte(*kubeconfigPayload), 0600)
	if err != nil {
		log.Error().Msgf("Failed to write temporary kubeconfig: %v", err)
		os.Exit(1)
	}
	defer os.Remove(tmpKubeconfigPath)

	// \todo test kubectl being runnable first

	// Execute kubectl command.
	kubectlArgs := []string{
		fmt.Sprintf("--kubeconfig=%s", tmpKubeconfigPath),
		fmt.Sprintf("--namespace=%s", kubernetesNamespace),
		"debug",
		targetPodName,
		"-it",
		"--profile=general",
		"--image=metaplay/diagnostics:latest",
		"--target=shard-server",
	}

	// Run with syscall.Exec() to run kubectl interactively.
	log.Info().Msgf("Execute: kubectl %s", strings.Join(kubectlArgs, " "))
	execCmd := exec.Command("kubectl", kubectlArgs...)
	execCmd.Stdin = os.Stdin
	execCmd.Stdout = os.Stdout
	execCmd.Stderr = os.Stderr
	if err := execCmd.Run(); err != nil {
		log.Error().Msgf("Failed to execute 'kubectl': %v", err)
		os.Exit(1)
	}

	return nil
}
