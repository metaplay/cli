# Metaplay CLI

## Description

The `metaplay` command-line tool is used to manage projects using Metaplay, to build and deploy the game server into the cloud, and to interact with the cloud environments in various ways.

## Installation

\todo Describe installation steps when we know them.

## Usage

### Authentication

To sign in using your browser via the Metaplay portal:

```bash
metaplay auth login
```

To sign in as a machine user (primarily for CI use cases), first set the `METAPLAY_CREDENTIALS` environment to the credentials from the [Metaplay portal](https://portal.metaplay.dev):

```bash
export METAPLAY_CREDENTIALS=<credentials>
metaplay auth machine-login
```

To sign out:

```bash
metaplay auth logout
```

### Build and Deploy Server to Cloud

1. Build server docker image:

    When running in the directory that has the `.metaplay.yaml` project config file:

    ```bash
    metaplay project build-image --image-tag=<fullImageName>
    ```

    When running in another directory:

    ```bash
    metaplay project -p <pathToProject> build-image --image-tag=<fullImageTag>
    ```

2. Push docker image to environment's docker image repository:

    ```bash
    metaplay environment -e <environmentID> push-image --image-tag=<fullImageName>
    ```

3. Deploy the game server with the pushed image:

    ```bash
    metaplay environment -e <environmentID> deploy-server --image-tag=<imageTagOnly>
    ```

### Kubernetes Access

To access the Kubernetes control plane for your environment, you can do the following:

```bash
# Get the kubeconfig file for the environment.
metaplay environment -e <environmentID> get-kubeconfig -o <pathToKubeconfig>
# Configure kubectl to use the kubeconfig file.
export KUBECONFIG=<pathToKubeconfig>
# Check the status of your pods.
kubectl get pods
# Get the logs from a specific pod.
kubectl logs <podName>
```

### Using in CI Jobs

For detailed instructions on how to set up your CI system, see the [Getting Started with Cloud Deployments](https://docs.metaplay.io/cloud-deployments/getting-started.html) guide.

### Troubleshooting

If you have any issues running a command, give it the `--verbose` flag to get more detailed output on what is happening.

If you have a paid support contract with Metaplay, you can open a ticket on the [Metaplay portal's support page](https://portal.metaplay.dev/orgs/metaplay/support).

## License

This module and all files within are distributed under the Metaplay SDK Software License.
