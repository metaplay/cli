/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package envapi

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: appsv1.StatefulSetSpec{
			Replicas: &replicas,
			Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
				Containers: []corev1.Container{{Name: "shard-server", Image: testRepository + ":" + tag}},
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
			Labels:          map[string]string{"controller-revision-hash": shardSet + "-rev2"},
			OwnerReferences: []metav1.OwnerReference{{Kind: "StatefulSet", Name: shardSet}},
		},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "shard-server", Image: testRepository + ":" + tag}}},
		Status: corev1.PodStatus{
			Phase:             corev1.PodRunning,
			ContainerStatuses: []corev1.ContainerStatus{{Name: "shard-server", State: state, Ready: ready}},
		},
	}
}

func TestEvaluateGameServerReadiness(t *testing.T) {
	running := corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}
	crashing := corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"}}

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
	restarted := testPod("all", 0, "new", running, false)
	restarted.Status.ContainerStatuses[0].RestartCount = 2

	tests := []struct {
		name      string
		target    string
		shardSets []appsv1.StatefulSet
		pods      []corev1.Pod
		ready     bool
		lines     []string
		failedPod string
	}{
		{
			// The operator has not yet applied the new image. The previous deployment's
			// pod is ready, but must not count.
			name:      "previous image",
			target:    "new",
			shardSets: []appsv1.StatefulSet{testShardSet("all", 1, "old")},
			pods:      []corev1.Pod{testPod("all", 0, "old", running, true)},
			lines:     []string{"Shard set 'all': waiting for the operator to apply the new image"},
		},
		{
			name:      "ready",
			target:    "new",
			shardSets: []appsv1.StatefulSet{testShardSet("logic", 2, "new"), testShardSet("all", 1, "new")},
			pods: []corev1.Pod{
				testPod("all", 0, "new", running, true),
				testPod("logic", 0, "new", running, true),
				testPod("logic", 1, "new", running, true),
			},
			ready: true,
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
			ready:     true,
			lines:     []string{"Shard set 'all': 1/1 pods ready", "  all-0  Ready"},
		},
		{
			name:  "no shard sets",
			lines: []string{"Waiting for the operator to create the shard sets"},
		},
		{
			name:      "stopping the previous deployment",
			target:    "new",
			shardSets: []appsv1.StatefulSet{testShardSet("all", 0, "new")},
			lines:     []string{"Shard set 'all': stopping the previous deployment"},
		},
		{
			name:      "pods missing, from a previous revision, or starting",
			target:    "new",
			shardSets: []appsv1.StatefulSet{testShardSet("all", 1, "new"), testShardSet("logic", 2, "new")},
			pods:      []corev1.Pod{previousRevision, testPod("logic", 1, "new", running, false)},
			lines: []string{
				"Shard set 'all': 0/1 pods ready",
				"  all-0  Pending  Previous pod is waiting to be replaced",
				"Shard set 'logic': 0/2 pods ready",
				"  logic-0  Pending  Pod not created yet",
				"  logic-1  Running  Server is starting",
			},
		},
		{
			name:      "unschedulable",
			target:    "new",
			shardSets: []appsv1.StatefulSet{testShardSet("all", 1, "new")},
			pods:      []corev1.Pod{unschedulable},
			lines: []string{
				"Shard set 'all': 0/1 pods ready",
				"  all-0  Pending  Cannot be scheduled: 0/3 nodes are available: 3 node(s) didn't match Pod's node affinity/selector.",
			},
		},
		{
			name:      "restarted",
			target:    "new",
			shardSets: []appsv1.StatefulSet{testShardSet("all", 1, "new")},
			pods:      []corev1.Pod{restarted},
			lines:     []string{"Shard set 'all': 0/1 pods ready", "  all-0  Running  Server is starting (restarted 2 times)"},
		},
		{
			name:      "crashing",
			target:    "new",
			shardSets: []appsv1.StatefulSet{testShardSet("all", 1, "new")},
			pods:      []corev1.Pod{testPod("all", 0, "new", crashing, false)},
			lines:     []string{"Shard set 'all': 0/1 pods ready", "  all-0  Failed   Server keeps crashing (CrashLoopBackOff)"},
			failedPod: "all-0",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := evaluateGameServerReadiness(tt.target, tt.shardSets, tt.pods)
			assert.Equal(t, tt.ready, got.ready)
			assert.Equal(t, tt.lines, got.lines)
			assert.Equal(t, tt.failedPod, got.failedPod)
			if tt.ready {
				assert.Empty(t, got.problems)
			} else {
				assert.NotEmpty(t, got.problems)
			}
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
