package cmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/metaplay/cli/pkg/auth"
	"github.com/metaplay/cli/pkg/helmutil"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var flagPodName string

// environmentDebugServerCmd starts a Kubernetes ephemeral debug container within the
// specified pod.
var environmentDebugServerCmd = &cobra.Command{
	Use:   "debug-server",
	Short: "Start a debug container targeting the specified pod",
	Run:   runDebugServerCmd,
}

func init() {
	environmentCmd.AddCommand(environmentDebugServerCmd)

	environmentDebugServerCmd.Flags().StringVar(&flagPodName, "pod-name", "", "Name of the pod to debug")
}

func runDebugServerCmd(cmd *cobra.Command, args []string) {
	// Load project config.
	// _, projectConfig, err := resolveProjectConfig()
	// if err != nil {
	// 	log.Error().Msgf("Failed to find project: %v", err)
	// 	os.Exit(1)
	// }

	// Ensure we have fresh tokens.
	tokenSet, err := auth.EnsureValidTokenSet()
	if err != nil {
		log.Error().Msgf("Failed to get credentials: %v", err)
		os.Exit(1)
	}

	// Resolve target environment.
	targetEnv, err := resolveTargetEnvironment(tokenSet)
	if err != nil {
		log.Error().Msgf("Failed to resolve environment: %v", err)
		os.Exit(1)
	}

	// Get environment details.
	log.Debug().Msg("Get environment details")
	envDetails, err := targetEnv.GetDetails()
	if err != nil {
		log.Error().Msgf("failed to get environment details: %v", err)
		os.Exit(1)
	}

	// Get kubeconfig to access the environment.
	kubeconfigPayload, err := targetEnv.GetKubeConfigWithEmbeddedCredentials()
	log.Debug().Msgf("Resolved kubeconfig to access environment")

	// Configure Helm.
	actionConfig, err := helmutil.NewActionConfig(*kubeconfigPayload, envDetails.Deployment.KubernetesNamespace)
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

	// Create context
	ctx := context.Background()

	// Get running game server pods.
	kubernetesNamespace := envDetails.Deployment.KubernetesNamespace
	log.Debug().Msgf("Get running game server pods")
	pods, err := clientset.CoreV1().Pods(kubernetesNamespace).List(ctx, metav1.ListOptions{
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
	targetPodName := flagPodName
	if targetPodName == "" {
		if len(gameServerPods) == 1 {
			targetPodName = gameServerPods[0].Name
		} else {
			var podNames []string
			for _, pod := range gameServerPods {
				podNames = append(podNames, pod.Name)
			}
			log.Info().Msgf("Multiple game server pods running: %v", strings.Join(podNames, ", "))
			log.Error().Msgf("Specify which pod you want to debug with --pod-name=<name>")
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
		fmt.Fprintf(os.Stderr, "Failed to execute 'kubectl': %v\n", err)
		os.Exit(1)
	}
}
