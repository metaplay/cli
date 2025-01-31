# Metaplay CLI

## Description

The `metaplay` command-line tool is used to manage projects using Metaplay, to build and deploy the game server into the cloud, and to interact with the cloud environments in various ways.

## Installation

### Direct Download

You can find the latest release on our [Github releases page](https://github.com/metaplay/cli/releases/latest).

* We provide 64-bit builds for Linux, macOS (both Intel and Apple Silicon), and Windows.

* Download the correct archive for your OS and CPU architecture as indicated on the filename (e.g. `MetaplayCLI_0.1.0_Linux_x86_64.tar.gz`).

* Unpack the contents into a directory that is included in your `PATH` environment variable, or create a new directory and add it to your `PATH`.

* Now you can run the `metaplay` executable in your terminal and it will output further instructions. See section [Usage](https://github.com/metaplay/cli?tab=readme-ov-file#usage) for details.

### Using Homebrew

```bash
brew tap metaplay/homebrew-tap
brew install metaplay
```

### Using Scoop

```bash
scoop bucket add metaplay https://github.com/metaplay/scoop-bucket
scoop install metaplay
```

### Using Chocolatey

```bash
choco install metaplay
```

### Development Build

We do continuously update the latest development build from the `metaplay/cli` repository `main` branch and it can be found on the [releases page](https://github.com/metaplay/cli/releases/tag/0.0.0), but there are no quality guarantees whatsoever associated with it. The development build is primarily intended for our internal use and is made available for Github CI runners to run automated tests on (and with) without the need to always build from scratch.

Development builds do not currently perform any version checks (for the purpose of new release notifications), and the CLI `update` command is disabled on development builds as well.

It is highly recommended to use the latest official release, so should you decide to mess with development builds, proceed with extreme caution!

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
