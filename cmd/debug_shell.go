/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"context"
	"fmt"

	"github.com/metaplay/cli/internal/tui"
	"github.com/metaplay/cli/pkg/envapi"
	"github.com/metaplay/cli/pkg/kubeutil"
	"github.com/metaplay/cli/pkg/styles"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes/scheme"
)

// \todo Show instructions locally (based on which username server process runs on) instead of --rcfile

// debugShellOpts holds the options for the 'debug shell' command
type debugShellOpts struct {
	UsePositionalArgs

	// Environment and pod selection
	Environment string
	PodName     string

	// Container options
	ContainerName string
	Image         string
	Command       []string
	Interactive   bool
}

func init() {
	o := debugShellOpts{
		ContainerName: metaplayServerContainerName,
		Image:         "metaplay/diagnostics:latest",
		Command:       []string{"/bin/bash", "--rcfile", "/entrypoint.sh"},
		Interactive:   true,
	}

	args := o.Arguments()
	args.AddStringArgumentOpt(&o.Environment, "ENVIRONMENT", "Target environment, eg, 'tough-falcons'.")
	args.AddStringArgumentOpt(&o.PodName, "POD", "Target pod name, eg, 'all-0'.")

	cmd := &cobra.Command{
		Use:     "shell [ENVIRONMENT] [POD] [flags]",
		Aliases: []string{"sh"},
		Short:   "[preview] Start a debug container targeting the specified pod",
		Run:     runCommand(&o),
		Long: renderLong(&o, `
			PREVIEW: This command is in preview and subject to change

			Start a debug container targeting a game server pod in the specified environment.
			This command creates a Kubernetes ephemeral debug container that attaches to an existing
			game server pod, allowing you to inspect and troubleshoot the running server.

			If multiple game server pods are running in the environment, you must specify which pod
			to debug by providing its name as the second argument. If only one pod is running,
			the pod name is optional.

			The debug container uses the metaplay/diagnostics:latest image which contains various
			debugging and diagnostic tools. The container is attached to the shard-server container
			within the pod, giving you direct access to the game server process.

			{Arguments}
		`),
		Example: renderExample(`
			# Start a debug container in the 'tough-falcons' environment (when only one pod is running).
			metaplay debug shell tough-falcons

			# Start a debug container pod named 'service-0' in the environment 'tough-falcons'.
			metaplay debug shell tough-falcons service-0
		`),
	}

	debugCmd.AddCommand(cmd)
}

// Complete finishes parsing arguments for the command
func (o *debugShellOpts) Prepare(cmd *cobra.Command, args []string) error {
	return nil
}

// Run executes the command
func (o *debugShellOpts) Run(cmd *cobra.Command) error {
	// Try to resolve the project & auth provider.
	project, err := tryResolveProject()
	if err != nil {
		return err
	}

	// Resolve environment config.
	envConfig, tokenSet, err := resolveEnvironment(cmd.Context(), project, o.Environment)
	if err != nil {
		return err
	}

	// Resolve target environment & game server.
	targetEnv := envapi.NewTargetEnvironment(tokenSet, envConfig.StackDomain, envConfig.HumanID)
	gameServer, err := targetEnv.GetGameServer(cmd.Context())
	if err != nil {
		return err
	}

	// Resolve target pod (or ask for it if not defined).
	kubeCli, pod, err := resolveTargetPod(gameServer, o.PodName)
	if err != nil {
		return err
	}

	// Create and attach to debug container
	debugContainerName, cleanup, err := kubeutil.CreateDebugContainer(cmd.Context(), kubeCli, pod.Name, o.ContainerName, true, true, o.Command)
	if err != nil {
		return err
	}
	defer cleanup()

	// Setup IO streams
	ioStreams := IOStreams{
		In:     cmd.InOrStdin(),
		Out:    cmd.OutOrStdout(),
		ErrOut: cmd.ErrOrStderr(),
	}

	// Attach to the running shell in the container.
	return o.attachToContainer(cmd.Context(), kubeCli, pod.Name, debugContainerName, ioStreams)
}

// attachToContainer attaches to the debug container
func (o *debugShellOpts) attachToContainer(ctx context.Context, kubeCli *envapi.KubeClient, podName, containerName string, ioStreams IOStreams) error {
	log.Debug().Msgf("Attaching to ephemeral debug container")

	// Prepare the attach request
	req := kubeCli.RestClient.
		Post().
		Resource("pods").
		Name(podName).
		Namespace(kubeCli.Namespace).
		SubResource("attach").
		VersionedParams(&corev1.PodExecOptions{
			Container: containerName,
			Stdin:     o.Interactive,
			Stdout:    true,
			Stderr:    true,
			TTY:       o.Interactive,
		}, scheme.ParameterCodec)

	// Use shared remote command execution utility
	return execRemoteKubernetesCommand(ctx, kubeCli.RestConfig, req.URL(), ioStreams, o.Interactive, o.Interactive)
}

func resolveTargetPod(gameServer *envapi.TargetGameServer, podName string) (*envapi.KubeClient, *corev1.Pod, error) {
	if podName != "" {
		// Find the pod and associated kubeCli for the cluster the pod resides on.
		kubeCli, pod, err := gameServer.GetPod(podName)
		return kubeCli, pod, err
	} else {
		// Get all shards sets and pods from all clusters associated with the game server.
		shardSetsWithPods, err := gameServer.GetAllShardSetsWithPods()
		if err != nil {
			return nil, nil, err
		}

		// If only one pod in one cluster, return it.
		if len(shardSetsWithPods) == 1 && len(shardSetsWithPods[0].Pods) == 1 {
			shardSet := shardSetsWithPods[0]
			return shardSet.ShardSet.Cluster.KubeClient, &shardSet.Pods[0], nil
		}

		// Let the user choose the shardSet and pod.
		kubeCli, pod, err := chooseTargetShardAndPodDialog(shardSetsWithPods)
		return kubeCli, pod, err
	}
}

func chooseTargetShardAndPodDialog(shardSetsWithPods []envapi.ShardSetWithPods) (*envapi.KubeClient, *corev1.Pod, error) {
	if !tui.IsInteractiveMode() {
		return nil, nil, fmt.Errorf("interactive mode required for selecting target pod")
	}

	if len(shardSetsWithPods) == 0 {
		return nil, nil, fmt.Errorf("no stateful sets exist in the gameserver")
	}

	// Let the user choose the target shard set.
	selectedShardSet, err := tui.ChooseFromListDialog(
		"Select Target Shard Set",
		shardSetsWithPods,
		func(shardSet *envapi.ShardSetWithPods) (string, string) {
			// \todo show some useful status?
			return shardSet.ShardSet.Name, ""
		},
	)
	if err != nil {
		return nil, nil, err
	}

	log.Info().Msgf(" %s %s", styles.RenderSuccess("✓"), selectedShardSet.ShardSet.Name)

	// Let the user choose the target shard pod in the shard set.
	selectedPod, err := tui.ChooseFromListDialog[corev1.Pod](
		"Select Target Pod",
		selectedShardSet.Pods,
		func(pod *corev1.Pod) (string, string) {
			return pod.Name, tui.GetPodDescription(pod)
		},
	)
	if err != nil {
		return nil, nil, err
	}

	log.Info().Msgf(" %s %s", styles.RenderSuccess("✓"), selectedPod.Name)
	log.Info().Msg("")

	return selectedShardSet.ShardSet.Cluster.KubeClient, selectedPod, nil
}
