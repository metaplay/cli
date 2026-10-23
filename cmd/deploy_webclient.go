/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	clierrors "github.com/metaplay/cli/internal/errors"
	"github.com/metaplay/cli/internal/tui"
	"github.com/metaplay/cli/pkg/envapi"
	"github.com/metaplay/cli/pkg/sitedeploy"
	"github.com/metaplay/cli/pkg/styles"
)

// Deploy a web client, or any static site, to the environment's public bucket,
// which its CDN serves.
type deployWebClientOpts struct {
	UsePositionalArgs

	argEnvironment  string
	argName         string
	flagForce       bool
	flagDryRun      bool
	flagConcurrency int
}

func init() {
	o := deployWebClientOpts{}

	args := o.Arguments()
	args.AddStringArgumentOpt(&o.argEnvironment, "ENVIRONMENT", "Target environment name or id, eg, 'lovely-wombats-build-nimbly'.")
	args.AddStringArgumentOpt(&o.argName, "NAME", "Name of the web client in metaplay-project.yaml, if the project has several.")

	cmd := &cobra.Command{
		Use:     "webclient [ENVIRONMENT] [NAME] [flags]",
		Aliases: []string{"web-client", "webclients"},
		Short:   "[preview] Deploy a web game client to the environment's CDN",
		Run:     runCommand(&o),
		Long: renderLong(&o, `
			PREVIEW: This command is in preview and subject to change!

			Upload a web client to the target environment's CDN. The environment decides
			where the web client goes: today that is the root of the environment's public
			CDN, https://<environment>-assets.<domain>/, and an environment that hosts web
			clients elsewhere says where in its details. The command prints the URL. It
			uploads the files in the web client's sourceDir as they are, so build the web
			client first.

			Configure web clients in the 'webClients' section of metaplay-project.yaml. For
			example, with the Metaplay SDK's preset for Blazor WebAssembly clients:

			  webClients:
			    - name: webclient
			      sourceDir: WebClient/bin/Release/net10.0-browser/publish/wwwroot
			      preset: blazor

			Fields of a web client:
			- name: identifies the web client in this command.
			- sourceDir: directory of the built files, relative to metaplay-project.yaml.
			- preset: the name of one of the Metaplay SDK's presets to start from: 'blazor'
			  for Blazor WebAssembly clients. A preset is a YAML file of the fields below
			  except prefix, and the SDK has its presets in <sdkRootDir>/WebClientPresets/.
			- presetFile: a preset file of the project's own to start from, instead of an
			  SDK preset, relative to metaplay-project.yaml.
			- prefix: path under the environment's web client location to upload to, eg,
			  'staging/'. Usually left out.
			- files: rules for how the files in sourceDir become objects in the bucket. A
			  file follows the first rule that matches its path, and is uploaded under its
			  own name if none does. A rule can remove a suffix from the name (stripSuffix),
			  set the Content-Encoding of the file's bytes (contentEncoding), or leave the
			  file out (exclude). Where two files would become the same object, as
			  app.css.br and app.css do below, the file the earlier rule matched is
			  uploaded, and a file no rule matched loses to one that a rule did:

			    files:
			      - match: "**/*.br"
			        stripSuffix: .br
			        contentEncoding: br
			      - match: "**/*.gz"
			        exclude: true

			- headers: rules that set the Content-Type and Cache-Control of the objects whose
			  name they match. For each header, the first matching rule that sets it wins:

			    headers:
			      - match: "art/**"
			        cacheControl: public, max-age=86400

			  Without a rule, Content-Type comes from the object's name and Cache-Control is
			  'no-cache'.
			- entryFiles: globs of the objects to upload last, in this order, such as
			  index.html. A page loaded during a deploy then finds the objects these name.
			- endpointsFile: name of a JSON file to write next to the web client, for the
			  client to read at startup. It holds the game server's address and the CDN's,
			  as far as the environment reports them: serverHost, serverPortForWebSocket,
			  enableTls, cdnBaseUrl and publicWebApiUrl. A field the environment does not
			  report is left out, and no file is written where it reports no game server.
			  The SDK's 'blazor' preset sets it to 'metaplay-endpoints.json', the name the
			  SDK's client reads.

			A rule matches by a glob (match) or a regular expression (matchRegex). A web
			client's settings override its preset's: its files and headers rules are checked
			before the preset's, and its entryFiles and endpointsFile replace the preset's.
			Use --dry-run to see the resulting configuration and the headers of each object.

			A deploy uploads only the files that changed since the last deploy, and never
			deletes files from the bucket. It records the files it uploaded in
			'<prefix>.metaplay-site.json'.

			The bucket also holds the game server's files, so the command refuses to upload
			under the names the game server uses, such as GameConfig/ and Volatile/.

			{Arguments}

			Related commands:
			- 'metaplay get environment-info ...' to see the environment's CDN address.
		`),
		Example: renderExample(`
			# Deploy the project's web client to environment nimbly.
			metaplay deploy webclient nimbly

			# Show the configuration and the headers of each file, without uploading anything.
			metaplay deploy webclient nimbly --dry-run

			# Deploy the web client named 'webclient', when the project has several.
			metaplay deploy webclient nimbly webclient
		`),
	}
	deployCmd.AddCommand(cmd)

	flags := cmd.Flags()
	flags.BoolVar(&o.flagForce, "force", false, "Upload all files, ignoring what the last deploy uploaded")
	flags.BoolVar(&o.flagDryRun, "dry-run", false, "Show what would be uploaded without uploading anything")
	flags.IntVar(&o.flagConcurrency, "concurrency", 16, "Number of files to upload at once")
}

func (o *deployWebClientOpts) Prepare(cmd *cobra.Command, args []string) error {
	if o.flagConcurrency < 1 {
		return fmt.Errorf("--concurrency must be at least 1")
	}
	return nil
}

func (o *deployWebClientOpts) Run(cmd *cobra.Command) error {
	// Resolve the project and the web client in it.
	project, err := resolveProject()
	if err != nil {
		return err
	}
	webClient, err := project.FindWebClient(o.argName)
	if err != nil {
		return err
	}
	siteDir := project.GetWebClientSourceDir(webClient)
	if info, err := os.Stat(siteDir); err != nil || !info.IsDir() {
		return clierrors.Newf("The web client's sourceDir '%s' is not a directory", siteDir).
			WithSuggestion("Build the web client first")
	}

	// The project config was validated when loaded.
	config, err := webClient.Resolve(project.RelativeDir, project.GetSdkRootDir())
	if err != nil {
		return clierrors.Wrapf(err, "Failed to apply the web client's preset").
			WithSuggestion("Name one of the SDK's presets with 'preset', or point 'presetFile' at a file relative to metaplay-project.yaml")
	}
	opts, err := config.Options()
	if err != nil {
		return err
	}

	// Resolve environment.
	envConfig, tokenSet, err := resolveEnvironment(cmd.Context(), project, o.argEnvironment)
	if err != nil {
		return err
	}

	log.Info().Msg("")
	log.Info().Msg(styles.RenderTitle("Deploy Web Client to Cloud"))
	log.Info().Msg("")

	targetEnv := envapi.NewTargetEnvironment(tokenSet, envConfig.StackDomain, envConfig.HumanID)
	envDetails, err := targetEnv.GetDetails()
	if err != nil {
		return clierrors.Wrap(err, "Failed to get the environment's details")
	}
	// Web clients are uploaded with the storage credentials the stack issues,
	// which not every stack does yet.
	s3Client, err := targetEnv.NewS3Client(envDetails)
	if errors.Is(err, envapi.ErrNoStorageCredentials) {
		return clierrors.Newf("Environment '%s' issues no credentials for uploading to its public bucket", envConfig.Name).
			WithDetails("Deploying web clients is not supported on this environment's infrastructure yet.")
	}
	if err != nil {
		return clierrors.Wrap(err, "Failed to access the environment's public bucket")
	}
	target, err := resolveWebClientTarget(&envDetails.Deployment, config.Prefix)
	if errors.Is(err, errNoWebClientHosting) {
		return clierrors.Newf("Environment '%s' has no public bucket and CDN to deploy to", envConfig.Name).
			WithSuggestion("Deploy the web client to a main environment: edge environments have no CDN")
	}
	if err != nil {
		return err
	}
	prefix := target.Prefix
	store := sitedeploy.NewS3Store(s3Client, target.Bucket)

	var previous *sitedeploy.Manifest
	if !o.flagForce {
		previous, err = sitedeploy.ReadManifest(cmd.Context(), store, prefix)
		if err != nil {
			return clierrors.Wrap(err, "Failed to read what the last deploy uploaded").
				WithSuggestion("Use --force to upload all files and start a new manifest")
		}
	}

	generated := map[string][]byte{}
	if config.EndpointsFile != "" {
		endpoints, err := webClientEndpoints(&envDetails.Deployment)
		if err != nil {
			return err
		}
		if endpoints != nil {
			generated[config.EndpointsFile] = endpoints
		} else {
			log.Warn().Msgf("Not writing %s: the environment reports no game server address", config.EndpointsFile)
		}
	}

	plan, err := sitedeploy.BuildPlan(siteDir, prefix, opts, generated, previous)
	if err != nil {
		return clierrors.Wrap(err, "Failed to plan the upload")
	}

	log.Info().Msg("Target environment:")
	log.Info().Msgf("  Name:          %s", styles.RenderTechnical(envConfig.Name))
	log.Info().Msgf("  ID:            %s", styles.RenderTechnical(envConfig.HumanID))
	log.Info().Msgf("  Bucket:        %s", styles.RenderTechnical(target.Bucket+"/"+prefix))
	log.Info().Msgf("  URL:           %s", styles.RenderTechnical(target.URL))
	log.Info().Msg("Web client:")
	log.Info().Msgf("  Name:          %s", styles.RenderTechnical(webClient.Name))
	log.Info().Msgf("  Source:        %s", styles.RenderTechnical(siteDir))
	log.Info().Msgf("  Files:         %s", styles.RenderTechnical(fmt.Sprintf("%d, of which %d changed", len(plan.Objects), plan.UploadCount())))
	log.Info().Msg("")

	if o.flagDryRun {
		configYAML, err := yaml.Marshal(config)
		if err != nil {
			return err
		}
		log.Info().Msg("Configuration, with its preset applied:")
		for _, line := range strings.Split(strings.TrimRight(string(configYAML), "\n"), "\n") {
			log.Info().Msgf("  %s", line)
		}
		log.Info().Msg("")
		log.Info().Msg("Files, in upload order:")
		for _, obj := range plan.Objects {
			action := "upload   "
			if obj.Unchanged {
				action = "unchanged"
			}
			encoding := ""
			if obj.ContentEncoding != "" {
				encoding = ", " + obj.ContentEncoding
			}
			generatedNote := ""
			if obj.Data != nil {
				generatedNote = ", generated"
			}
			log.Info().Msgf("  %s %s %s", styles.RenderMuted(action), obj.Key,
				styles.RenderMuted(fmt.Sprintf("(%d bytes, %s%s, %s%s)", obj.Size, obj.ContentType, encoding, obj.CacheControl, generatedNote)))
		}
		for name, data := range generated {
			log.Info().Msg("")
			log.Info().Msgf("%s:", name)
			for _, line := range strings.Split(string(data), "\n") {
				log.Info().Msgf("  %s", line)
			}
		}
		log.Info().Msg("")
		log.Info().Msg(styles.RenderSuccess("✅ Dry run: nothing was uploaded"))
		return nil
	}

	progress := tui.NewCountProgress(os.Stderr, tui.IsInteractiveTerminal(os.Stderr))
	_, err = sitedeploy.Deploy(cmd.Context(), store, webClient.Name, plan, previous, time.Now().UTC(), o.flagConcurrency, func(done, total int) {
		progress.Update("Uploading files", done, total)
	})
	progress.Finish(err)
	if err != nil {
		return clierrors.Wrap(err, "Failed to deploy the web client").
			WithSuggestion("Run the command again to finish the upload")
	}

	log.Info().Msg("")
	log.Info().Msg(styles.RenderSuccess("✅ Successfully deployed the web client"))
	log.Info().Msgf("Open it at %s", styles.RenderTechnical(target.URL))
	return nil
}

// webClientEndpointsFile is the endpoints file of a web client: how the client
// reaches its game server and CDN. Its fields are those of the SDK's
// ConnectionEndpointConfig.
type webClientEndpointsFile struct {
	ServerHost             string `json:"serverHost"`
	ServerPortForWebSocket int    `json:"serverPortForWebSocket,omitempty"`
	EnableTLS              *bool  `json:"enableTls,omitempty"`
	CdnBaseURL             string `json:"cdnBaseUrl,omitempty"`
	PublicWebAPIURL        string `json:"publicWebApiUrl,omitempty"`
}

// webClientEndpoints writes the endpoints file of a web client from what the
// environment reports, and only that: a field it does not report is left out,
// for the client to fill in itself. Nil where it reports no game server.
func webClientEndpoints(deployment *envapi.Deployment) ([]byte, error) {
	if deployment.ServerHostname == "" {
		return nil, nil
	}
	endpoints := webClientEndpointsFile{ServerHost: deployment.ServerHostname}
	if len(deployment.ServerWebSocketPorts) > 0 {
		endpoints.ServerPortForWebSocket = deployment.ServerWebSocketPorts[0]
	}
	if deployment.ServerTLSCert != "" {
		enableTLS := true
		endpoints.EnableTLS = &enableTLS
	}
	if deployment.CdnS3Fqdn != "" {
		endpoints.CdnBaseURL = "https://" + deployment.CdnS3Fqdn + "/"
	}
	if deployment.PublicWebApiURL != "" {
		endpoints.PublicWebAPIURL = strings.TrimSuffix(deployment.PublicWebApiURL, "/") + "/"
	}
	return json.MarshalIndent(endpoints, "", "  ")
}

// errNoWebClientHosting reports an environment with nowhere to host a web
// client, such as an edge environment, which has no bucket or CDN.
var errNoWebClientHosting = errors.New("the environment has no bucket and CDN for web clients")

// webClientTarget is where a web client is uploaded, and where it is then
// served.
type webClientTarget struct {
	Bucket string // Bucket the web client's objects go to.
	Prefix string // Key prefix of the objects, ending in a slash, or empty for the bucket root.
	URL    string // URL the prefix is served at, ending in a slash.
}

// resolveWebClientTarget finds where the environment hosts the web client
// whose prefix in metaplay-project.yaml is clientPrefix. The environment
// decides the location, so a project does not change when its hosting does:
// where the stack names a web client location, it is that, and elsewhere the
// root of the environment's public CDN. clientPrefix is a path under it.
func resolveWebClientTarget(deployment *envapi.Deployment, clientPrefix string) (*webClientTarget, error) {
	var base webClientTarget
	if deployment.ClientURL != "" {
		basePrefix, err := sitedeploy.NormalizePrefix(deployment.ClientS3Prefix)
		if err != nil {
			return nil, fmt.Errorf("the environment names an invalid web client prefix: %w", err)
		}
		bucket := deployment.ClientS3Bucket
		if bucket == "" {
			bucket = deployment.S3BucketPublic
		}
		base = webClientTarget{Bucket: bucket, Prefix: basePrefix, URL: strings.TrimSuffix(deployment.ClientURL, "/") + "/"}
	} else if deployment.CdnS3Fqdn != "" {
		base = webClientTarget{Bucket: deployment.S3BucketPublic, URL: "https://" + deployment.CdnS3Fqdn + "/"}
	}
	if base.Bucket == "" {
		return nil, errNoWebClientHosting
	}

	prefix, err := sitedeploy.NormalizePrefix(clientPrefix)
	if err != nil {
		return nil, err
	}
	return &webClientTarget{Bucket: base.Bucket, Prefix: base.Prefix + prefix, URL: base.URL + prefix}, nil
}
