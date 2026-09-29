// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package docker

import "context"

// StackOps abstracts stack operations for testability and extensibility.
type StackOps interface {
	RemoveStack(ctx context.Context, stackName string) error
	RemoveStackNetworks(ctx context.Context, stackName string) error
	DeployStack(stackName string, yamlContent string, opts DeployOptions) error
	ValidateStackYAML(content string) error
	InspectStack(stackName string) (string, error)
	ReconstructStackCompose(stackName string) (string, error)
}

type defaultStackOps struct{}

func (defaultStackOps) RemoveStack(ctx context.Context, stackName string) error {
	return RemoveStack(ctx, stackName)
}

func (defaultStackOps) RemoveStackNetworks(ctx context.Context, stackName string) error {
	return RemoveStackNetworks(ctx, stackName)
}

func (defaultStackOps) DeployStack(stackName string, yamlContent string, opts DeployOptions) error {
	return deployStack(stackName, yamlContent, opts)
}

func (defaultStackOps) ValidateStackYAML(content string) error {
	return ValidateStackYAML(content)
}

func (defaultStackOps) InspectStack(stackName string) (string, error) {
	return GetStackInspection(stackName)
}

func (defaultStackOps) ReconstructStackCompose(stackName string) (string, error) {
	return ReconstructStackCompose(stackName)
}
