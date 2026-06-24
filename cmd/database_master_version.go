/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/metaplay/cli/pkg/envapi"
	"github.com/rs/zerolog/log"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes/scheme"
)

// queryDatabaseMasterVersion reads the current database master version from a database's MetaInfo
// table (the highest-Version row), connecting through the given host via a mariadb client running in
// the provided debug pod. Returns nil when it can't be determined (e.g. the MetaInfo table doesn't
// exist on a fresh database). Best-effort: it must never fail the calling command.
func queryDatabaseMasterVersion(ctx context.Context, kubeCli *envapi.KubeClient, podName, debugContainerName, host, user, password, dbName string) *int {
	// Pipe the query via stdin (avoids shell quoting). -N skips column names, -B uses batch
	// (tab-separated) output, so a successful query prints just the integer value.
	mariadbCmd := fmt.Sprintf("mariadb -h %s -u %s -p%s -N -B %s", host, user, password, dbName)

	const query = "SELECT MasterVersion FROM MetaInfo ORDER BY Version DESC LIMIT 1;"

	req := kubeCli.Clientset.CoreV1().
		RESTClient().
		Post().
		Resource("pods").
		Name(podName).
		Namespace(kubeCli.Namespace).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: debugContainerName,
			Command:   []string{"/bin/sh", "-c", mariadbCmd},
			Stdin:     true,
			Stdout:    true,
			Stderr:    true,
			TTY:       false,
		}, scheme.ParameterCodec)

	// Capture stdout; discard stderr so a missing MetaInfo table doesn't print a scary error.
	var outputBuffer bytes.Buffer
	ioStreams := IOStreams{
		In:     strings.NewReader(query),
		Out:    &outputBuffer,
		ErrOut: io.Discard,
	}

	if err := execRemoteKubernetesCommand(ctx, kubeCli.RestConfig, req.URL(), ioStreams, false, false); err != nil {
		log.Debug().Err(err).Msg("Could not query database master version (continuing without it)")
		return nil
	}

	output := strings.TrimSpace(outputBuffer.String())
	if output == "" || strings.EqualFold(output, "NULL") {
		log.Debug().Msg("Database master version not available (empty MetaInfo result)")
		return nil
	}

	masterVersion, err := strconv.Atoi(output)
	if err != nil {
		log.Debug().Str("output", output).Msg("Could not parse database master version (continuing without it)")
		return nil
	}

	return &masterVersion
}
