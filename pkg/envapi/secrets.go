/*
 * Copyright Metaplay. All rights reserved.
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
	// Initialize a Kubernetes clientset against the environment
	clientset, err := targetEnv.NewKubernetesClientSet()
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
	_, err = clientset.CoreV1().Secrets(targetEnv.HumanId).Create(ctx, secret, metav1.CreateOptions{})
	return err
}

// DeleteSecret deletes a Kubernetes secret with the given name
func (targetEnv *TargetEnvironment) DeleteSecret(ctx context.Context, name string) error {
	clientset, err := targetEnv.NewKubernetesClientSet()
	if err != nil {
		return err
	}

	// Get the secret to check its labels
	secret, err := clientset.CoreV1().Secrets(targetEnv.HumanId).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to retrieve secret: %w", err)
	}

	// Check that the secret is a valid user secret
	if value, ok := secret.Labels[userSecretLabelName]; !ok || value != userSecretLabelValue {
		return fmt.Errorf("secret %s is not a valid user secret", name)
	}

	// Delete the secret
	return clientset.CoreV1().Secrets(targetEnv.HumanId).Delete(ctx, name, metav1.DeleteOptions{})
}

// GetSecret retrieves a Kubernetes secret by name
func (targetEnv *TargetEnvironment) GetSecret(ctx context.Context, name string) (*corev1.Secret, error) {
	clientset, err := targetEnv.NewKubernetesClientSet()
	if err != nil {
		return nil, err
	}

	// Get the secret
	secret, err := clientset.CoreV1().Secrets(targetEnv.HumanId).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	// Check that the secret is a valid user secret
	if value, ok := secret.Labels[userSecretLabelName]; !ok || value != userSecretLabelValue {
		return nil, fmt.Errorf("secret %s is not a valid user secret", name)
	}

	return secret, nil
}

// ListSecrets lists all Kubernetes secrets with the label foo=bar
func (targetEnv *TargetEnvironment) ListSecrets(ctx context.Context) ([]corev1.Secret, error) {
	clientset, err := targetEnv.NewKubernetesClientSet()
	if err != nil {
		return nil, err
	}

	labelSelector := fmt.Sprintf("%s=%s", userSecretLabelName, userSecretLabelValue)
	secrets, err := clientset.CoreV1().Secrets(targetEnv.HumanId).List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	if err != nil {
		return nil, err
	}

	return secrets.Items, nil
}
