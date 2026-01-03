/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package envapi

import (
	"context"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Required name prefix for all user secrets (to avoid conflicts with built-in secrets).
const userSecretNamePrefix = "user-"

// Label name and value to tag on all user secrets to distinguish them from other secrets.
const userSecretLabelName = "io.metaplay.secret-type"
const userSecretLabelValue = "user"

func (targetEnv *TargetEnvironment) CreateSecret(ctx context.Context, name string, payloadValues map[string][]byte) error {
	// Initialize a Kubernetes kubeCli against the environment
	kubeCli, err := targetEnv.GetPrimaryKubeClient()
	if err != nil {
		return err
	}

	// Check that the secret name starts with the required prefix
	if !strings.HasPrefix(name, userSecretNamePrefix) {
		return fmt.Errorf("secret names must start with the prefix '%s'", userSecretNamePrefix)
	}

	// Secret contents.
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			Labels: map[string]string{userSecretLabelName: userSecretLabelValue},
		},
		Data: payloadValues,
	}

	// Create the secret.
	_, err = kubeCli.Clientset.CoreV1().Secrets(kubeCli.Namespace).Create(ctx, secret, metav1.CreateOptions{})
	return err
}

// DeleteSecret deletes a Kubernetes secret with the given name
func (targetEnv *TargetEnvironment) DeleteSecret(ctx context.Context, name string) error {
	// Initialize a Kubernetes kubeCli against the environment
	kubeCli, err := targetEnv.GetPrimaryKubeClient()
	if err != nil {
		return err
	}

	// Get the secret to check its labels
	secret, err := kubeCli.Clientset.CoreV1().Secrets(kubeCli.Namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to retrieve secret: %w", err)
	}

	// Check that the secret is a valid user secret
	if value, ok := secret.Labels[userSecretLabelName]; !ok || value != userSecretLabelValue {
		return fmt.Errorf("secret %s is not a valid user secret", name)
	}

	// Delete the secret
	return kubeCli.Clientset.CoreV1().Secrets(kubeCli.Namespace).Delete(ctx, name, metav1.DeleteOptions{})
}

// GetSecret retrieves a Kubernetes secret by name
func (targetEnv *TargetEnvironment) GetSecret(ctx context.Context, name string) (*corev1.Secret, error) {
	// Initialize a Kubernetes kubeCli against the environment
	kubeCli, err := targetEnv.GetPrimaryKubeClient()
	if err != nil {
		return nil, err
	}

	// Get the secret
	secret, err := kubeCli.Clientset.CoreV1().Secrets(kubeCli.Namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	// Check that the secret is a valid user secret
	if value, ok := secret.Labels[userSecretLabelName]; !ok || value != userSecretLabelValue {
		return nil, fmt.Errorf("secret %s is not a valid user secret", name)
	}

	return secret, nil
}

// ListSecrets lists all Kubernetes secrets with the user secret label.
// If no secrets exist, an empty list is returned.
func (targetEnv *TargetEnvironment) ListSecrets(ctx context.Context) ([]corev1.Secret, error) {
	// Initialize a Kubernetes kubeCli against the environment.
	kubeCli, err := targetEnv.GetPrimaryKubeClient()
	if err != nil {
		return nil, err
	}

	// Fetch the secrets with the appropriate label from Kubernetes.
	labelSelector := fmt.Sprintf("%s=%s", userSecretLabelName, userSecretLabelValue)
	secrets, err := kubeCli.Clientset.CoreV1().Secrets(targetEnv.HumanID).List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	if err != nil {
		return nil, err
	}

	// Return empty list instead of nil.
	if secrets.Items == nil {
		return []corev1.Secret{}, nil
	}

	return secrets.Items, nil
}

// UpdateSecret updates an existing Kubernetes secret with new data.
// The newData map replaces the entire secret data.
func (targetEnv *TargetEnvironment) UpdateSecret(ctx context.Context, name string, newData map[string][]byte) error {
	// Initialize a Kubernetes kubeCli against the environment
	kubeCli, err := targetEnv.GetPrimaryKubeClient()
	if err != nil {
		return err
	}

	// Get the existing secret
	secret, err := kubeCli.Clientset.CoreV1().Secrets(kubeCli.Namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to retrieve secret: %w", err)
	}

	// Check that the secret is a valid user secret
	if value, ok := secret.Labels[userSecretLabelName]; !ok || value != userSecretLabelValue {
		return fmt.Errorf("secret %s is not a valid user secret", name)
	}

	// Update the secret data
	secret.Data = newData

	// Update the secret in Kubernetes
	_, err = kubeCli.Clientset.CoreV1().Secrets(kubeCli.Namespace).Update(ctx, secret, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update secret: %w", err)
	}

	return nil
}
