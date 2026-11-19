/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package envapi

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func TestBuildHealthCheckPacket(t *testing.T) {
	packet := buildHealthCheckPacket()

	// Wire packet: [flags:1][payloadSize:3][payload:4]
	// flags = 0x04 (WirePacketType.HealthCheck), payloadSize = 4
	// payload = 0x00000001 (HealthCheckTypeBits.GlobalState)
	expected := []byte{0x04, 0x00, 0x00, 0x04, 0x00, 0x00, 0x00, 0x01}

	if len(packet) != len(expected) {
		t.Fatalf("expected packet length %d, got %d", len(expected), len(packet))
	}
	for i := range expected {
		if packet[i] != expected[i] {
			t.Errorf("byte %d: expected 0x%02x, got 0x%02x", i, expected[i], packet[i])
		}
	}
}

func TestParseProtocolHeader(t *testing.T) {
	tests := []struct {
		name    string
		data    []byte
		wantErr bool
		version byte
		status  byte
	}{
		{
			name:    "valid header",
			data:    []byte{0x4D, 0x50, 0x41, 0x53, 10, 3, 0x00, 0x00},
			wantErr: false,
			version: 10,
			status:  3,
		},
		{
			name:    "too short",
			data:    []byte{0x4D, 0x50, 0x41},
			wantErr: true,
		},
		{
			name:    "longer than 8 bytes (extra data ignored)",
			data:    []byte{0x4D, 0x50, 0x41, 0x53, 10, 3, 0x00, 0x00, 0xFF, 0xFF},
			wantErr: false,
			version: 10,
			status:  3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info, err := parseProtocolHeader(tt.data)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if info.Version != tt.version {
				t.Errorf("version: expected %d, got %d", tt.version, info.Version)
			}
			if info.Status != tt.status {
				t.Errorf("status: expected %d, got %d", tt.status, info.Status)
			}
		})
	}
}

func TestValidateProtocolHeader(t *testing.T) {
	tests := []struct {
		name    string
		header  protocolHeaderInfo
		wantErr bool
		errMsg  string
	}{
		{
			name: "ClusterRunning",
			header: protocolHeaderInfo{
				Version: 10,
				Status:  protocolStatusClusterRunning,
			},
			wantErr: false,
		},
		{
			name: "ClusterStarting",
			header: protocolHeaderInfo{
				Version: 10,
				Status:  protocolStatusClusterStarting,
			},
			wantErr: true,
			errMsg:  "server cluster is still starting",
		},
		{
			name: "ClusterShuttingDown",
			header: protocolHeaderInfo{
				Version: 10,
				Status:  protocolStatusClusterShuttingDown,
			},
			wantErr: true,
			errMsg:  "server cluster is shutting down",
		},
		{
			name: "wrong version",
			header: protocolHeaderInfo{
				Version: 5,
				Status:  protocolStatusClusterRunning,
			},
			wantErr: true,
			errMsg:  "unexpected protocol version",
		},
		{
			name: "unknown status",
			header: protocolHeaderInfo{
				Version: 10,
				Status:  99,
			},
			wantErr: true,
			errMsg:  "unexpected protocol status",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateProtocolHeader(tt.header)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if tt.errMsg != "" && !strings.Contains(err.Error(), tt.errMsg) {
					t.Errorf("error %q should contain %q", err.Error(), tt.errMsg)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

const testRepository = "123.dkr.ecr.eu-west-1.amazonaws.com/game-gameserver"

// testShardSet returns a shard set running the given image tag.
func testShardSet(name string, replicas int32, tag string) appsv1.StatefulSet {
	return appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: name, UID: types.UID(name + "-uid")},
		Spec: appsv1.StatefulSetSpec{
			Replicas: &replicas,
			Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
				Containers: []corev1.Container{{Name: shardServerContainerName, Image: testRepository + ":" + tag}},
			}},
		},
		Status: appsv1.StatefulSetStatus{UpdateRevision: name + "-rev2"},
	}
}

// testPod returns a pod of a shard set at its latest revision, running the given image
// tag, with its server container in the given state.
func testPod(shardSet string, ndx int, tag string, state corev1.ContainerState, ready bool) corev1.Pod {
	return corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:            fmt.Sprintf("%s-%d", shardSet, ndx),
			UID:             types.UID(fmt.Sprintf("pod-%s-%d", shardSet, ndx)),
			Labels:          map[string]string{"controller-revision-hash": shardSet + "-rev2"},
			OwnerReferences: []metav1.OwnerReference{{Kind: "StatefulSet", Name: shardSet, UID: types.UID(shardSet + "-uid")}},
		},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: shardServerContainerName, Image: testRepository + ":" + tag}}},
		Status: corev1.PodStatus{
			Phase:             corev1.PodRunning,
			ContainerStatuses: []corev1.ContainerStatus{{Name: shardServerContainerName, State: state, Ready: ready}},
		},
	}
}

func TestEvaluateGameServerReadiness(t *testing.T) {
	running := corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}
	crashing := corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"}}
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)

	unschedulable := testPod("all", 0, "new", corev1.ContainerState{}, false)
	unschedulable.Status = corev1.PodStatus{
		Phase: corev1.PodPending,
		Conditions: []corev1.PodCondition{{
			Type:    corev1.PodScheduled,
			Status:  corev1.ConditionFalse,
			Reason:  corev1.PodReasonUnschedulable,
			Message: "0/3 nodes are available: 3 node(s) didn't match Pod's node affinity/selector. preemption: 0/3 nodes are available: 3 Preemption is not helpful for scheduling.",
		}},
	}
	previousRevision := testPod("all", 0, "new", running, true)
	previousRevision.Labels["controller-revision-hash"] = "all-rev1"
	// The operator recreates a StatefulSet under the same name: the pod of the previous
	// one has the same name, image and revision, but another owner UID.
	previousOwner := testPod("logic", 0, "new", running, true)
	previousOwner.OwnerReferences[0].UID = "logic-previous-uid"
	// restartedPod returns a pod whose server is running, not ready, and was restarted
	// the given number of times, the last time at the given time.
	restartedPod := func(restarts int32, startedAt time.Time) corev1.Pod {
		pod := testPod("all", 0, "new", corev1.ContainerState{Running: &corev1.ContainerStateRunning{StartedAt: metav1.NewTime(startedAt)}}, false)
		pod.Status.ContainerStatuses[0].RestartCount = restarts
		pod.Status.ContainerStatuses[0].LastTerminationState.Terminated = &corev1.ContainerStateTerminated{ExitCode: 137, Reason: "Error"}
		return pod
	}
	readyRestarted := testPod("all", 0, "new", running, true)
	readyRestarted.Status.ContainerStatuses[0].RestartCount = 1
	readyRestarted.Status.ContainerStatuses[0].LastTerminationState.Terminated = &corev1.ContainerStateTerminated{ExitCode: 137, Reason: "OOMKilled"}
	exited := testPod("all", 0, "new", corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 1, Reason: "Error"}}, false)
	imagePullBackOff := testPod("all", 0, "new", corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "ImagePullBackOff", Message: "Back-off pulling image"}}, false)
	containerCreating := testPod("all", 0, "new", corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "ContainerCreating"}}, false)
	staleShardSet := testShardSet("all", 1, "new")
	staleShardSet.Generation = 3
	staleShardSet.Status.ObservedGeneration = 2
	digestShardSet := testShardSet("all", 1, "new")
	digestShardSet.Spec.Template.Spec.Containers[0].Image = testRepository + "@sha256:abcd"
	digestPod := testPod("all", 0, "new", running, true)
	digestPod.Spec.Containers[0].Image = testRepository + "@sha256:abcd"

	tests := []struct {
		name      string
		target    readinessTarget
		shardSets []appsv1.StatefulSet
		pods      []corev1.Pod
		lines     []string
		problems  []string // None means ready
		failedPod string
		logs      podLogSource // Logs of failedPod to show
	}{
		{
			// The operator has not yet applied the new image. The previous deployment's
			// pod is ready, but must not count.
			name:      "previous image",
			target:    readinessTarget{imageTag: "new"},
			shardSets: []appsv1.StatefulSet{testShardSet("all", 1, "old")},
			pods:      []corev1.Pod{testPod("all", 0, "old", running, true)},
			lines:     []string{"Shard set 'all': waiting for the operator to apply the new image"},
			problems:  []string{"Shard set 'all' still runs image old, not new"},
		},
		{
			name:      "ready",
			target:    readinessTarget{imageTag: "new", shardSets: map[string]bool{"all": true, "logic": true}},
			shardSets: []appsv1.StatefulSet{testShardSet("logic", 2, "new"), testShardSet("all", 1, "new")},
			pods: []corev1.Pod{
				testPod("all", 0, "new", running, true),
				testPod("logic", 0, "new", running, true),
				testPod("logic", 1, "new", running, true),
			},
			lines: []string{
				"Shard set 'logic': 2/2 pods ready",
				"  logic-0  Ready",
				"  logic-1  Ready",
				"Shard set 'all': 1/1 pods ready",
				"  all-0  Ready",
			},
		},
		{
			name:      "no target image accepts any image",
			shardSets: []appsv1.StatefulSet{testShardSet("all", 1, "old")},
			pods:      []corev1.Pod{testPod("all", 0, "old", running, true)},
			lines:     []string{"Shard set 'all': 1/1 pods ready", "  all-0  Ready"},
		},
		{
			name:      "image pinned by digest only is accepted",
			target:    readinessTarget{imageTag: "new"},
			shardSets: []appsv1.StatefulSet{digestShardSet},
			pods:      []corev1.Pod{digestPod},
			lines:     []string{"Shard set 'all': 1/1 pods ready", "  all-0  Ready"},
		},
		{
			name:     "no shard sets",
			lines:    []string{"Waiting for the operator to create the shard sets"},
			problems: []string{"No shard sets found"},
		},
		{
			// A StatefulSet the CR does not declare, such as one left over from a removed
			// shard, is ignored. A declared shard set on an edge cluster need not exist.
			name:      "undeclared shard set ignored, declared one missing",
			target:    readinessTarget{imageTag: "new", shardSets: map[string]bool{"all": true, "logic": true, "edge": false}},
			shardSets: []appsv1.StatefulSet{testShardSet("all", 1, "new"), testShardSet("removed", 1, "old")},
			pods:      []corev1.Pod{testPod("all", 0, "new", running, true), testPod("removed", 0, "old", running, true)},
			lines: []string{
				"Shard set 'logic': waiting for the operator to create it",
				"Shard set 'all': 1/1 pods ready",
				"  all-0  Ready",
			},
			problems: []string{"Shard set 'logic' does not exist"},
		},
		{
			name:      "Kubernetes has not applied the shard set change",
			target:    readinessTarget{imageTag: "new"},
			shardSets: []appsv1.StatefulSet{staleShardSet},
			pods:      []corev1.Pod{testPod("all", 0, "new", running, true)},
			lines:     []string{"Shard set 'all': waiting for Kubernetes to apply the change"},
			problems:  []string{"Shard set 'all' has a change that Kubernetes has not applied yet"},
		},
		{
			name:      "stopping the previous deployment",
			target:    readinessTarget{imageTag: "new"},
			shardSets: []appsv1.StatefulSet{testShardSet("all", 0, "new")},
			lines:     []string{"Shard set 'all': stopping the previous deployment"},
			problems:  []string{"Shard set 'all' is still stopping the previous deployment"},
		},
		{
			name:      "pods missing, from a previous revision or StatefulSet, or starting",
			target:    readinessTarget{imageTag: "new"},
			shardSets: []appsv1.StatefulSet{testShardSet("all", 1, "new"), testShardSet("logic", 3, "new")},
			pods:      []corev1.Pod{previousRevision, previousOwner, testPod("logic", 1, "new", running, false)},
			lines: []string{
				"Shard set 'all': 0/1 pods ready",
				"  all-0  Pending  Previous pod is waiting to be replaced",
				"Shard set 'logic': 0/3 pods ready",
				"  logic-0  Pending  Previous pod is waiting to be replaced",
				"  logic-1  Running  Server is starting",
				"  logic-2  Pending  Pod not created yet",
			},
			problems: []string{
				"Pod all-0 (Pending): Previous pod is waiting to be replaced",
				"Pod logic-0 (Pending): Previous pod is waiting to be replaced",
				"Pod logic-1 (Running): Server is starting",
				"Pod logic-2 (Pending): Pod not created yet",
			},
		},
		{
			name:      "unschedulable",
			target:    readinessTarget{imageTag: "new"},
			shardSets: []appsv1.StatefulSet{testShardSet("all", 1, "new")},
			pods:      []corev1.Pod{unschedulable},
			lines: []string{
				"Shard set 'all': 0/1 pods ready",
				"  all-0  Pending  Cannot be scheduled: 0/3 nodes are available: 3 node(s) didn't match Pod's node affinity/selector.",
			},
			problems: []string{"Pod all-0 (Pending): Cannot be scheduled: 0/3 nodes are available: 3 node(s) didn't match Pod's node affinity/selector."},
		},
		{
			// A server that hangs at startup is killed by its liveness probe, and shows
			// as running again between the restarts.
			name:      "restarted twice while starting",
			target:    readinessTarget{imageTag: "new"},
			shardSets: []appsv1.StatefulSet{testShardSet("all", 1, "new")},
			pods:      []corev1.Pod{restartedPod(2, now.Add(-time.Minute))},
			lines:     []string{"Shard set 'all': 0/1 pods ready", "  all-0  Failed   Server failed to start (restarted 2 times, last exit code 137 (Error))"},
			problems:  []string{"Pod all-0 (Failed): Server failed to start (restarted 2 times, last exit code 137 (Error))"},
			failedPod: "all-0",
			logs:      previousPodLogs,
		},
		{
			// A server may exit once while what it depends on is starting too.
			name:      "restarted once while starting",
			target:    readinessTarget{imageTag: "new"},
			shardSets: []appsv1.StatefulSet{testShardSet("all", 1, "new")},
			pods:      []corev1.Pod{restartedPod(1, now.Add(-time.Minute))},
			lines:     []string{"Shard set 'all': 0/1 pods ready", "  all-0  Running  Server is starting (restarted 1 time, last exit code 137 (Error))"},
			problems:  []string{"Pod all-0 (Running): Server is starting (restarted 1 time, last exit code 137 (Error))"},
		},
		{
			// The restarts are from the server's past, and it has run since.
			name:      "not ready long after its restarts",
			target:    readinessTarget{imageTag: "new"},
			shardSets: []appsv1.StatefulSet{testShardSet("all", 1, "new")},
			pods:      []corev1.Pod{restartedPod(3, now.Add(-48*time.Hour))},
			lines:     []string{"Shard set 'all': 0/1 pods ready", "  all-0  Running  Server is starting (restarted 3 times, last exit code 137 (Error))"},
			problems:  []string{"Pod all-0 (Running): Server is starting (restarted 3 times, last exit code 137 (Error))"},
		},
		{
			name:      "exited",
			target:    readinessTarget{imageTag: "new"},
			shardSets: []appsv1.StatefulSet{testShardSet("all", 1, "new")},
			pods:      []corev1.Pod{exited},
			lines:     []string{"Shard set 'all': 0/1 pods ready", "  all-0  Failed   Server exited: Error (exit code 1)"},
			problems:  []string{"Pod all-0 (Failed): Server exited: Error (exit code 1)"},
			failedPod: "all-0",
			logs:      currentPodLogs,
		},
		{
			name:      "image cannot be pulled",
			target:    readinessTarget{imageTag: "new"},
			shardSets: []appsv1.StatefulSet{testShardSet("all", 1, "new")},
			pods:      []corev1.Pod{imagePullBackOff},
			lines:     []string{"Shard set 'all': 0/1 pods ready", "  all-0  Failed   Container cannot start: ImagePullBackOff: Back-off pulling image"},
			problems:  []string{"Pod all-0 (Failed): Container cannot start: ImagePullBackOff: Back-off pulling image"},
			failedPod: "all-0",
			logs:      noPodLogs,
		},
		{
			name:      "container being created",
			target:    readinessTarget{imageTag: "new"},
			shardSets: []appsv1.StatefulSet{testShardSet("all", 1, "new")},
			pods:      []corev1.Pod{containerCreating},
			lines:     []string{"Shard set 'all': 0/1 pods ready", "  all-0  Pending  Container is waiting: ContainerCreating"},
			problems:  []string{"Pod all-0 (Pending): Container is waiting: ContainerCreating"},
		},
		{
			name:      "ready after a restart",
			target:    readinessTarget{imageTag: "new"},
			shardSets: []appsv1.StatefulSet{testShardSet("all", 1, "new")},
			pods:      []corev1.Pod{readyRestarted},
			lines:     []string{"Shard set 'all': 1/1 pods ready", "  all-0  Ready    (restarted 1 time, last exit code 137 (OOMKilled))"},
		},
		{
			name:      "crashing",
			target:    readinessTarget{imageTag: "new"},
			shardSets: []appsv1.StatefulSet{testShardSet("all", 1, "new")},
			pods:      []corev1.Pod{testPod("all", 0, "new", crashing, false)},
			lines:     []string{"Shard set 'all': 0/1 pods ready", "  all-0  Failed   Server keeps crashing (CrashLoopBackOff)"},
			problems:  []string{"Pod all-0 (Failed): Server keeps crashing (CrashLoopBackOff)"},
			failedPod: "all-0",
			logs:      previousPodLogs,
		},
		{
			// A pod from before the deploy, which the operator may be about to replace,
			// does not fail the wait.
			name:      "crashing pod spared",
			target:    readinessTarget{imageTag: "new", sparedPods: map[types.UID]bool{"pod-all-0": true}},
			shardSets: []appsv1.StatefulSet{testShardSet("all", 1, "new")},
			pods:      []corev1.Pod{testPod("all", 0, "new", crashing, false)},
			lines:     []string{"Shard set 'all': 0/1 pods ready", "  all-0  Failed   Server keeps crashing (CrashLoopBackOff)"},
			problems:  []string{"Pod all-0 (Failed): Server keeps crashing (CrashLoopBackOff)"},
		},
		{
			name:      "crashing pod not spared",
			target:    readinessTarget{imageTag: "new", sparedPods: map[types.UID]bool{"pod-all-1": true}},
			shardSets: []appsv1.StatefulSet{testShardSet("all", 1, "new")},
			pods:      []corev1.Pod{testPod("all", 0, "new", crashing, false)},
			lines:     []string{"Shard set 'all': 0/1 pods ready", "  all-0  Failed   Server keeps crashing (CrashLoopBackOff)"},
			problems:  []string{"Pod all-0 (Failed): Server keeps crashing (CrashLoopBackOff)"},
			failedPod: "all-0",
			logs:      previousPodLogs,
		},
		{
			// When every declared shard set may run on an edge cluster, which is not
			// checked, there is nothing to wait for on the primary cluster.
			name:   "only edge shard sets declared",
			target: readinessTarget{imageTag: "new", shardSets: map[string]bool{"edge-1": false, "edge-2": false}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.target.now = now
			got := evaluateGameServerReadiness(tt.target, tt.shardSets, tt.pods)
			assert.Equal(t, tt.lines, got.lines)
			assert.Equal(t, tt.problems, got.problems)
			assert.Equal(t, len(tt.problems) == 0, got.ready())
			assert.Equal(t, tt.failedPod, got.failedPod)
			if tt.failedPod != "" {
				assert.Equal(t, tt.logs, got.failedPodLogs)
			}
		})
	}
}

func TestSparedPods(t *testing.T) {
	baseline := &DeployBaseline{podUIDs: map[types.UID]bool{"old-pod": true}}
	tests := []struct {
		name      string
		baseline  *DeployBaseline
		crChanged bool
		elapsed   time.Duration
		want      map[types.UID]bool
	}{
		{name: "no deploy", baseline: nil, crChanged: true, elapsed: 0, want: nil},
		{name: "deploy changed the CR", baseline: baseline, crChanged: true, elapsed: 10 * time.Second, want: baseline.podUIDs},
		{name: "grace period over", baseline: baseline, crChanged: true, elapsed: oldPodGracePeriod, want: nil},
		{name: "deploy changed nothing", baseline: baseline, crChanged: false, elapsed: 0, want: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, sparedPods(tt.baseline, tt.crChanged, tt.elapsed))
		})
	}
}

func TestSchedulerMessage(t *testing.T) {
	tests := []struct{ message, want string }{
		{
			message: "0/3 nodes are available: 3 Insufficient cpu. preemption: 0/3 nodes are available: 3 No preemption victims found for incoming pod.",
			want:    "0/3 nodes are available: 3 Insufficient cpu.",
		},
		{
			message: "0/93 nodes are available: 8 node(s) didn't match Pod's node affinity/selector, 85 node(s) had untolerated taint(s). no new claims to deallocate, preemption: 0/93 nodes are available: 93 Preemption is not helpful for scheduling.",
			want:    "0/93 nodes are available: 8 node(s) didn't match Pod's node affinity/selector, 85 node(s) had untolerated taint(s).",
		},
		{
			message: "no nodes available to schedule pods",
			want:    "no nodes available to schedule pods",
		},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, schedulerMessage(tt.message))
	}
}

func TestImageTag(t *testing.T) {
	tests := map[string]string{
		"repo:tag":               "tag",
		"registry:5000/repo:tag": "tag",
		"registry:5000/repo":     "",
		"repo@sha256:abcd":       "",
		"repo:tag@sha256:abcd":   "tag",
		"":                       "",
	}
	for image, want := range tests {
		assert.Equal(t, want, imageTag(image), image)
	}
}
