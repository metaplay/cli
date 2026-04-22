/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"os"
	"strings"

	"github.com/golang-jwt/jwt/v5"
	clierrors "github.com/metaplay/cli/internal/errors"
	"github.com/metaplay/cli/internal/version"
	"github.com/metaplay/cli/pkg/auth"
	"github.com/metaplay/cli/pkg/llmdocsclient"
	"github.com/metaplay/cli/pkg/styles"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

const defaultLLMDocsTarget = "llm-docs.platform.metaplay.dev:443"

var llmDocsCmd = &cobra.Command{
	Use:   "llm-docs",
	Short: "[preview] Query the Metaplay LLM-friendly documentation service (machine use only)",
}

func init() {
	llmDocsCmd.GroupID = "other"
	rootCmd.AddCommand(llmDocsCmd)
}

type llmDocsMetadata struct {
	AccessToken string
	UserID      string
	UserEmail   string
	SdkVersion  string
	ProjectID   string
}

// buildLLMDocsMetadata gathers best-effort metadata to attach to llm-docs
// requests. None of these are required; missing values are simply omitted.
func buildLLMDocsMetadata() llmDocsMetadata {
	meta := llmDocsMetadata{}

	// Project metadata from metaplay-project.yaml (if any).
	project, err := tryResolveProject()
	if err != nil {
		log.Debug().Msgf("llm-docs: skipping project metadata, project failed to load: %v", err)
	} else if project != nil {
		meta.ProjectID = project.Config.ProjectHumanID
		if project.VersionMetadata.SdkVersion != nil {
			meta.SdkVersion = project.VersionMetadata.SdkVersion.String()
		}
	}

	// Auth + user identity from the persisted Metaplay session (if any).
	// We deliberately use LoadSessionState (not LoadAndRefreshTokenSet) here:
	// a failed refresh would delete the session, and a best-effort metadata
	// read must never have that side effect.
	authProvider := auth.NewMetaplayAuthProvider()
	sessionState, err := auth.LoadSessionState(authProvider.GetSessionID())
	if err != nil {
		log.Debug().Msgf("llm-docs: skipping auth metadata, failed to load session: %v", err)
		return meta
	}
	if sessionState == nil {
		return meta
	}
	meta.AccessToken = sessionState.TokenSet.AccessToken
	meta.UserID, meta.UserEmail = userIdentityFromTokens(sessionState.TokenSet)
	return meta
}

func newLLMDocsClient(ctx context.Context) (*llmdocsclient.Client, error) {
	target := strings.TrimSpace(os.Getenv("METAPLAYCLI_LLM_DOCS_ADDR"))
	isOverrideTarget := target != ""
	if target == "" {
		target = defaultLLMDocsTarget
	}
	useInsecure := shouldUseInsecureLLMDocsTarget(target)

	dialOpts := []grpc.DialOption{
		grpc.WithUserAgent(fmt.Sprintf("MetaplayCLI/%s", version.AppVersion)),
		grpc.WithPerRPCCredentials(bearerCredentials{requireTLS: !useInsecure}),
	}
	if useInsecure {
		dialOpts = append(dialOpts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	} else {
		dialOpts = append(dialOpts, grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{
			MinVersion: tls.VersionTLS12,
		})))
	}

	if isOverrideTarget {
		transportMode := "tls"
		if useInsecure {
			transportMode = "insecure"
		}
		stderrLogger.Info().Msgf(styles.RenderMuted("llm-docs target: %s (%s)"), target, transportMode)
	}

	client, err := llmdocsclient.DialContext(ctx, target, dialOpts...)
	if err != nil {
		return nil, clierrors.Wrapf(err, "Failed to connect to llm-docs (%s)", target).
			WithSuggestion("Set METAPLAYCLI_LLM_DOCS_ADDR to override the gRPC target; loopback targets use plaintext automatically")
	}
	return client, nil
}

func shouldUseInsecureLLMDocsTarget(target string) bool {
	if isTruthy(os.Getenv("METAPLAYCLI_LLM_DOCS_INSECURE")) {
		return true
	}

	host := target
	if parsedHost, _, err := net.SplitHostPort(target); err == nil {
		host = parsedHost
	}

	if strings.EqualFold(host, "localhost") {
		return true
	}

	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// buildRequestMetadata returns the RequestMetadata message that should be
// attached to every llm-docs RPC body. All caller attribution (user email,
// project, SDK version) goes here; nothing app-level rides in gRPC headers.
func buildRequestMetadata() *llmdocsclient.RequestMetadata {
	meta := buildLLMDocsMetadata()
	rm := &llmdocsclient.RequestMetadata{}
	if meta.UserEmail != "" {
		rm.UserEmail = &meta.UserEmail
	}
	if meta.ProjectID != "" {
		rm.ProjectId = &meta.ProjectID
	}
	if meta.SdkVersion != "" {
		rm.SdkVersion = &meta.SdkVersion
	}
	return rm
}

// bearerCredentials implements grpc.PerRPCCredentials to attach the bearer
// token from the persisted Metaplay session to every outgoing RPC. The token
// is fetched lazily per call so a refreshed session is picked up without
// redialing.
type bearerCredentials struct {
	requireTLS bool
}

func (c bearerCredentials) GetRequestMetadata(ctx context.Context, _ ...string) (map[string]string, error) {
	meta := buildLLMDocsMetadata()
	if meta.AccessToken == "" {
		return nil, nil
	}
	return map[string]string{"authorization": "Bearer " + meta.AccessToken}, nil
}

func (c bearerCredentials) RequireTransportSecurity() bool {
	return c.requireTLS
}

func wrapLLMDocsError(err error, action string) error {
	if err == nil {
		return nil
	}

	st, ok := status.FromError(err)
	if !ok {
		return clierrors.Wrapf(err, "Failed to %s via llm-docs", action)
	}

	switch st.Code() {
	case codes.InvalidArgument:
		return clierrors.Newf("Invalid llm-docs request while trying to %s", action).
			WithDetails(st.Message())
	case codes.NotFound:
		return clierrors.Newf("llm-docs returned not found while trying to %s", action).
			WithDetails(st.Message()).
			WithSuggestion("Check the path; use 'metaplay llm-docs read index.md' to see the catalog")
	case codes.FailedPrecondition:
		return clierrors.Newf("llm-docs could not %s", action).
			WithDetails(st.Message())
	default:
		return clierrors.Wrapf(err, "Failed to %s via llm-docs", action)
	}
}

// userIdentityFromTokens returns the user's subject and email, decoded
// locally from the JWTs in the token set. Prefers the ID token (which has
// richer profile claims) and falls back to the access token for `sub`.
// Returns empty strings on any decode failure.
func userIdentityFromTokens(tokenSet *auth.TokenSet) (sub, email string) {
	for _, raw := range []string{tokenSet.IDToken, tokenSet.AccessToken} {
		if raw == "" {
			continue
		}
		parsed, _, err := jwt.NewParser().ParseUnverified(raw, jwt.MapClaims{})
		if err != nil {
			continue
		}
		claims, ok := parsed.Claims.(jwt.MapClaims)
		if !ok {
			continue
		}
		if sub == "" {
			if v, _ := claims["sub"].(string); v != "" {
				sub = v
			}
		}
		if email == "" {
			if v, _ := claims["email"].(string); v != "" {
				email = v
			}
		}
		if sub != "" && email != "" {
			break
		}
	}
	return sub, email
}
