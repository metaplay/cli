/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package cmd

import (
	"bufio"
	"container/heap"
	"context"
	"fmt"
	"strings"
	"time"

	clierrors "github.com/metaplay/cli/internal/errors"
	"github.com/metaplay/cli/pkg/envapi"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
)

// Number of entries to buffer for each pod.
const logEntryBufferSize = 100

// Name of the game server container.
const metaplayServerContainerName = "shard-server"

type debugLogsOpts struct {
	UsePositionalArgs

	argEnvironment string
	flagPodName    string        // Show logs from the specified pod only
	flagSince      time.Duration // Show logs since X duration ago
	flagSinceTime  string        // Show logs since the specified timestamp (RFC3339)
	flagFollow     bool          // Keep streaming logs in until terminated
	sinceTime      *time.Time    // Parsed flagSinceTime (or nil of flagSinceTime is empty)
}

func init() {
	o := debugLogsOpts{}

	args := o.Arguments()
	args.AddStringArgumentOpt(&o.argEnvironment, "ENVIRONMENT", "Target environment name or id, eg, 'tough-falcons'.")

	cmd := &cobra.Command{
		Use:   "logs [ENVIRONMENT] [flags]",
		Short: "Show logs from one or more game server pods",
		Run:   runCommand(&o),
		Long: renderLong(&o, `
			Show logs from one or more game server pods in the target environment.

			{Arguments}

			Related commands:
			- 'metaplay deploy server ...' to deploy a game server into the cloud.
		`),
		Example: renderExample(`
			# Show logs from environment 'tough-falcons' up until now.
			metaplay debug logs tough-falcons

			# Show logs and keep streaming them until terminated.
			metaplay debug logs tough-falcons -f

			# Show logs only from the 'service-0' pod.
			metaplay debug logs tough-falcons --pod service-0

			# Show logs more recent than 3 hours.
			metaplay debug logs tough-falcons --since=3h

			# Show logs since Dec 27th, 2024 15:04:05 UTC.
			metaplay debug logs tough-falcons --since-time=2024-12-27T15:04:05Z
		`),
	}

	debugCmd.AddCommand(cmd)

	// Register flags
	flags := cmd.Flags()
	flags.StringVar(&o.flagPodName, "pod", "", "Show logs only from the pod matching this name.")
	flags.DurationVar(&o.flagSince, "since", 0, "Show logs more recent than specified duration like 30s, 15m, or 3h. Defaults to all logs.")
	flags.StringVar(&o.flagSinceTime, "since-time", "", "Show logs more recent than specified timestamp. Defaults to all logs.")
	flags.BoolVarP(&o.flagFollow, "follow", "f", false, "Keep streaming logs from pods until terminated.")
}

func (o *debugLogsOpts) Prepare(cmd *cobra.Command, args []string) error {
	// --since and --since-time are mutually exclusive.
	if o.flagSince != 0 && o.flagSinceTime != "" {
		return clierrors.NewUsageError("Cannot use both --since and --since-time").
			WithSuggestion("Use only one of --since or --since-time")
	}

	// Parse --since-time (if specified).
	if o.flagSinceTime != "" {
		t, err := time.Parse(time.RFC3339, o.flagSinceTime)
		if err != nil {
			return clierrors.WrapUsageError(err, "Invalid --since-time format").
				WithSuggestion("Use RFC3339 format, e.g., '2024-12-27T15:04:05Z'")
		}
		o.sinceTime = &t
	}

	return nil
}

func (o *debugLogsOpts) Run(cmd *cobra.Command) error {
	if o.flagSince != 0 {
		log.Debug().Msgf("Since: %v", o.flagSince)
	}

	// Try to resolve the project & auth provider.
	project, err := tryResolveProject()
	if err != nil {
		return err
	}

	// Resolve environment.
	envConfig, tokenSet, err := resolveEnvironment(cmd.Context(), project, o.argEnvironment)
	if err != nil {
		return err
	}

	// Create TargetEnvironment.
	targetEnv := envapi.NewTargetEnvironment(tokenSet, envConfig.StackDomain, envConfig.HumanID)

	// Create a Kubernetes client.
	// \todo support multi-region
	kubeCli, err := targetEnv.GetPrimaryKubeClient()
	if err != nil {
		return err
	}

	// Resolve the game server pods in the environment.
	// \todo Keep updating the list of pods to dynamically adapt to new/delete pods.
	pods, err := envapi.FetchGameServerPods(cmd.Context(), kubeCli)
	if err != nil {
		return clierrors.Wrap(err, "Failed to find game server pods").
			WithSuggestion("Make sure you have deployed a game server to this environment")
	}
	if len(pods) == 0 {
		return clierrors.New("No game server pods found in the environment").
			WithSuggestion("Deploy a game server first with 'metaplay deploy server'")
	}
	log.Debug().Msgf("Found %d game server pods: %s", len(pods), strings.Join(getPodNames(pods), ", "))

	// Filter the server pods if --pod is specified.
	if o.flagPodName != "" {
		filteredPods := []corev1.Pod{}
		for _, pod := range pods {
			if pod.Name == o.flagPodName {
				filteredPods = append(filteredPods, pod)
			}
		}

		if len(filteredPods) == 0 {
			return clierrors.Newf("No game server pods match the name '%s'", o.flagPodName).
				WithSuggestion("Check available pods with 'kubectl get pods' or omit --pod to see all pods")
		}

		pods = filteredPods
		log.Debug().Msgf("Filtered game server pods to: %s", strings.Join(getPodNames(pods), ", "))
	}

	// Stream logs from the pods.
	return o.readOrderedLogs(cmd.Context(), kubeCli, pods)
}

func (o *debugLogsOpts) readOrderedLogs(ctx context.Context, kubeCli *envapi.KubeClient, pods []corev1.Pod) error {
	// Use current time as the cut-off time between historical and real-time streaming logs.
	cutoffTime := time.Now().UTC()
	log.Debug().Msgf("Use cutoff time: %s", cutoffTime)

	// Start reading the historical logs from each time -- read until cutoffTime.
	historicalSources := o.readHistoricalLogsFromPods(ctx, kubeCli, pods, cutoffTime)

	// Start reading/following the realtime logs from each pod, starting from cutoffTime.
	var realtimeSources []*podLogSource
	if o.flagFollow {
		realtimeSources = readRealtimeLogsFromPods(ctx, kubeCli, pods, cutoffTime)
	}

	// Aggregate historical source while merging the sources in timestamp order (until completion).
	aggregateHistoricalLogsInTimeOrder(historicalSources)
	log.Debug().Msgf("Switch from historical to realtime logs")

	// Next, aggregate real-time sources with a small time-window for merging sources in timestamp order.
	if o.flagFollow {
		aggregateRealtimeSourcesInTimeOrder(realtimeSources)
	}

	return nil
}

func readPodLogsWithOpts(ctx context.Context, kubeCli *envapi.KubeClient, pods []corev1.Pod, logOpts *corev1.PodLogOptions, cutoffTime *time.Time) []*podLogSource {
	// Determine longest prefix name (to keep the prefixes aligned).
	longestPrefixName := getLongestPodPrefix(pods)

	// Create logs request for realtime entries from each pod.
	sources := make([]*podLogSource, len(pods))
	for ndx, pod := range pods {
		req := kubeCli.Clientset.CoreV1().Pods(kubeCli.Namespace).GetLogs(pod.Name, logOpts)
		channel := make(chan LogEntry, logEntryBufferSize)
		prefix := rightPad(fmt.Sprintf("%s:", pod.Name), longestPrefixName+1)
		sources[ndx] = &podLogSource{
			prefix:  prefix,
			request: req,
			channel: channel,
		}
	}

	// Read historical logs from each pod in parallel.
	for _, source := range sources {
		go readPodLogs(ctx, source, cutoffTime)
	}

	return sources
}

func (o *debugLogsOpts) readHistoricalLogsFromPods(ctx context.Context, kubeCli *envapi.KubeClient, pods []corev1.Pod, cutoffTime time.Time) []*podLogSource {
	// Log options for historical entries.
	var sinceSecondsPtr *int64 = nil
	if o.flagSince != 0 {
		sinceSeconds := int64(o.flagSince.Seconds())
		sinceSecondsPtr = &sinceSeconds
	}

	var sinceTimePtr *metav1.Time
	if o.sinceTime != nil {
		sinceTimePtr = &metav1.Time{Time: *o.sinceTime}
	}

	opts := &corev1.PodLogOptions{
		Follow:       false,
		Container:    metaplayServerContainerName,
		Timestamps:   true,
		SinceSeconds: sinceSecondsPtr,
		SinceTime:    sinceTimePtr,
	}

	return readPodLogsWithOpts(ctx, kubeCli, pods, opts, &cutoffTime)
}

func readRealtimeLogsFromPods(ctx context.Context, kubeCli *envapi.KubeClient, pods []corev1.Pod, cutoffTime time.Time) []*podLogSource {
	// Log options for realtime entries.
	opts := &corev1.PodLogOptions{
		Follow:     true,
		Container:  metaplayServerContainerName,
		SinceTime:  &metav1.Time{Time: cutoffTime},
		Timestamps: true,
	}

	// Read the logs from the pods.
	return readPodLogsWithOpts(ctx, kubeCli, pods, opts, nil)
}

type podLogSource struct {
	prefix  string        // Name of the source (eg, pod name).
	request *rest.Request // REST request to read data from.
	channel chan LogEntry // Channel of log entries.
}

type LogEntry struct {
	timestamp time.Time
	message   string
}

// readPodLogs reads log entries from the source, parses them (i.e., extracts the
// timestamp), and writes to the source's channel. The reading stops when the
// cutoffTime (if non-nil) is reached.
func readPodLogs(ctx context.Context, source *podLogSource, cutoffTime *time.Time) {
	defer close(source.channel) // close channel when done reading logs

	// Open a stream to read log entries from Kubernetes.
	stream, err := source.request.Stream(ctx)
	if err != nil {
		log.Error().Msgf("Failed to open stream for pod %s: %v", source.prefix, err)
		return
	}
	defer stream.Close()

	// Kubernetes logs with 'Timestamps: true' are of the format "<timestamp> <message>",
	// for example: "2024-12-23T15:04:05.999999999Z some log text".
	// Create a scanner with increased buffer size to handle very long lines
	scanner := bufio.NewScanner(bufio.NewReader(stream))
	scannerBufSize := 1 * 1024 * 1024 // 1MB buffer
	buf := make([]byte, scannerBufSize)
	scanner.Buffer(buf, scannerBufSize)

	for scanner.Scan() {
		// Parse the timestamp and payload from the line. We assume Kubernetes format.
		line := scanner.Text()
		parts := strings.SplitN(line, " ", 2)
		if len(parts) < 2 {
			log.Warn().Msgf("Malformed line from pod %s: '%s'", source.prefix, line)
			continue
		}
		tsStr, msg := parts[0], parts[1]

		// Parse timestamp
		timestamp, err := time.Parse(time.RFC3339Nano, tsStr)
		if err != nil {
			log.Warn().Msgf("Invalid timestamp '%s' from pod %s: %v", tsStr, source.prefix, err)
			continue
		}

		// If entry is later-or-equal than cutoffTime, we're done.
		if cutoffTime != nil {
			if timestamp.Compare(*cutoffTime) >= 0 {
				return
			}
		}

		entry := LogEntry{
			timestamp: timestamp,
			message:   msg,
		}

		// Send entry to aggregator (or bail out if operation canceled).
		select {
		case source.channel <- entry:
		case <-ctx.Done():
			// if context is canceled, exit early
			return
		}
	}

	// Handle scanner errors.
	if err := scanner.Err(); err != nil {
		log.Error().Msgf("Scanner error for pod %s: %v", source.prefix, err)
	}
}

type entryWithSource struct {
	entry     LogEntry // Log entry
	sourceNdx int      // Source index
}

// min-heap for entryWithSource, sorted by entry.Timestamp
type logEntryHeap []entryWithSource

func (h logEntryHeap) Len() int           { return len(h) }
func (h logEntryHeap) Less(i, j int) bool { return h[i].entry.timestamp.Before(h[j].entry.timestamp) }
func (h logEntryHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *logEntryHeap) Push(x any)        { *h = append(*h, x.(entryWithSource)) }
func (h *logEntryHeap) Pop() any {
	old := *h
	n := len(old)
	item := old[n-1]
	*h = old[:n-1]
	return item
}

// aggregateHistoricalLogsInTimeOrder merges multiple channels of LogEntry in ascending timestamp order.
func aggregateHistoricalLogsInTimeOrder(sources []*podLogSource) {
	// Initialize a min-heap for log entries
	var pq logEntryHeap
	heap.Init(&pq)

	// For each channel, do a *blocking* read of the first entry.
	for sourceNdx, source := range sources {
		// Read one log entry from the channel (BLOCKING)
		entry, ok := <-source.channel
		if ok {
			// We got an entry; push it into the heap
			heap.Push(&pq, entryWithSource{
				entry:     entry,
				sourceNdx: sourceNdx,
			})
		}
		// If !ok, the channel is closed or had no data; skip it
	}

	// Main loop: keep popping from the heap until it is empty
	for pq.Len() > 0 {
		// Pop the earliest log entry
		earliest := heap.Pop(&pq).(entryWithSource)
		entrySource := sources[earliest.sourceNdx]

		// Output (or process) the earliest entry
		log.Info().Msgf("%s%s", entrySource.prefix, earliest.entry.message)

		// Read the next entry from the same channel (block until value is available or channel is closed)
		nextEntry, ok := <-entrySource.channel
		if ok {
			// Push the new entry into the heap
			heap.Push(&pq, entryWithSource{
				entry:     nextEntry,
				sourceNdx: earliest.sourceNdx,
			})
		}
		// If !ok, that channel is closed/exhausted, so we do not re-push anything
	}

	// When the heap is empty, we're done merging all channels
	// (all channels are either exhausted or never produced a line).
}

// Aggregate real-time logs from sources in best-effort timestamp order.
// A small time window is used for keeping the items in timestamp order but
// in the event of network latencies larger than the window, it's possible
// that entries are printed out-of-order.
// \todo Optimize the memory usage by not fetching only one entry per source at a time.
// This also fixes a potential misordering of entries if they have the same timestamp
// (heap provides no guarantees of stable ordering of items with identical priority).
func aggregateRealtimeSourcesInTimeOrder(sources []*podLogSource) {
	// Initialize a min-heap for log entries
	var pq logEntryHeap
	heap.Init(&pq)

	// Track how many channels are still open
	activeChannels := len(sources)

	// Periodically check for new messages and flush old ones
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()

	// Main loop runs until all channels are closed AND the heap is empty
	for activeChannels > 0 || pq.Len() > 0 {
		// 1) Attempt non-blocking reads from each source
		for ndx, src := range sources {
			// If the channel is already "nil", it's closed, skip it
			if src.channel == nil {
				continue
			}

			// Read as many events as are immediately available from the source.
		drainLoop:
			for {
				select {
				case e, ok := <-src.channel:
					if !ok {
						// Channel closed
						sources[ndx].channel = nil
						activeChannels--
						break
					}
					// Push any received entry into the min-heap
					heap.Push(&pq, entryWithSource{
						entry:     e,
						sourceNdx: ndx,
					})
				default:
					// No more data right now (non-blocking read would block)
					break drainLoop
				}
				// If the channel closed in this iteration, we break out
				if src.channel == nil {
					break
				}
			}
		}

		// 2) Flush old events from the heap
		//    We pop entries older than "time.Now() - 1s"
		now := time.Now()
		cutoff := now.Add(-1 * time.Second)

		// Keep popping while there's an entry older than our cutoff
		for pq.Len() > 0 {
			oldest := pq[0] // peek at the earliest event
			if oldest.entry.timestamp.Before(cutoff) {
				popped := heap.Pop(&pq).(entryWithSource)
				log.Info().Msgf("%s%s", sources[popped.sourceNdx].prefix, popped.entry.message)
			} else {
				// The earliest event is still within the 1-second window,
				// so we wait for the next iteration in case something older arrives.
				break
			}
		}

		// 3) Wait until the next ticker interval
		//    This prevents busy-looping and gives time for new events to arrive.
		<-ticker.C
	}

	// When we exit the loop, activeChannels == 0 (all closed) and pq is empty
	// so there's nothing left to print.
}

// getPodNames extracts the names of each pod in the input array.
func getPodNames(pods []corev1.Pod) []string {
	names := make([]string, len(pods))
	for ndx, pod := range pods {
		names[ndx] = pod.Name
	}
	return names
}

// Suffix a string with enough spaces to be of the specified length.
func rightPad(s string, length int) string {
	if len(s) >= length {
		return s
	}
	return s + strings.Repeat(" ", length-len(s))
}

func getLongestPodPrefix(pods []corev1.Pod) int {
	longestPrefixName := 0
	for _, pod := range pods {
		longestPrefixName = max(longestPrefixName, len(pod.Name))
	}
	return longestPrefixName
}
