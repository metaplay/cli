/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package skills

import (
	"errors"
	"fmt"
)

// InstallRequest is the high-level input to RunInstall. It captures
// everything the orchestrator needs to resolve scope and targets and then
// hand off to the lower-level Install function.
//
// Library callers typically build one InstallRequest and pass it through;
// the cobra command in this repo and any third-party adapter do the same
// thing, just with their own flag-parsing on top.
type InstallRequest struct {
	// Skills is the set to install. Pass LoadAll(EmbeddedFS()) for the
	// canonical bundled set, or any LoadAll result for custom payloads.
	Skills []*Skill

	// Scope, when non-nil, forces the install scope. When nil, the
	// orchestrator asks Prompter; if Prompter is also nil it defaults to
	// ScopeProject.
	Scope *Scope

	// ProjectDir is the absolute root for ScopeProject (typically the
	// project working directory or a -p / --project override). Required
	// when Scope may resolve to ScopeProject.
	ProjectDir string

	// UserDir is the absolute root for ScopeUser (typically
	// os.UserHomeDir()). Tests pass t.TempDir() to keep the real home
	// directory untouched. Required when Scope may resolve to ScopeUser.
	UserDir string

	// TargetIDs lists the AgentDir IDs to install into. When empty the
	// orchestrator either asks Prompter, or in non-interactive mode
	// installs into every detected dir (or DefaultAgentDirID if none).
	TargetIDs []string

	// Version is stamped into each wrapper's metaplay-cli-version field.
	// Required.
	Version string

	// Force overrides the version gate. Existing user-authored files
	// (without managed-by) are still preserved.
	Force bool

	// DevMode also overrides the version gate; logically separate so
	// adapters can present different messaging.
	DevMode bool

	// Prompter, if non-nil, drives interactive resolution of unset Scope
	// and empty TargetIDs.
	Prompter Prompter
}

// InstallResult carries the resolved scope/root/targets alongside the per-
// (skill, target) action records produced by the underlying Install call.
//
// The resolved fields let adapters echo what they decided ("Scope: User —
// /home/x") to their UI without re-deriving from request inputs.
type InstallResult struct {
	Scope   Scope
	RootDir string
	Targets []AgentDir
	Actions []InstallAction
}

// RunInstall is the high-level install orchestrator: it resolves scope and
// targets (interactively when Prompter is supplied, otherwise from inputs
// and detection), then runs Install. Returns the resolved decisions plus
// the action records.
func RunInstall(req InstallRequest) (InstallResult, error) {
	if req.Version == "" {
		return InstallResult{}, errors.New("version is required")
	}
	if len(req.Skills) == 0 {
		return InstallResult{}, errors.New("no skills to install")
	}

	scope, err := resolveInstallScope(req)
	if err != nil {
		return InstallResult{}, err
	}

	rootDir, err := pickRootDir(scope, req.ProjectDir, req.UserDir)
	if err != nil {
		return InstallResult{}, err
	}

	targets, err := resolveTargets(req.TargetIDs, scope, rootDir, req.Prompter)
	if err != nil {
		return InstallResult{}, err
	}

	if err := ValidateTargetsForScope(targets, scope); err != nil {
		return InstallResult{}, err
	}

	actions, err := Install(InstallOptions{
		Skills:  req.Skills,
		Targets: targets,
		RootDir: rootDir,
		Scope:   scope,
		Version: req.Version,
		Force:   req.Force,
		DevMode: req.DevMode,
	})
	if err != nil {
		return InstallResult{}, err
	}

	return InstallResult{
		Scope:   scope,
		RootDir: rootDir,
		Targets: targets,
		Actions: actions,
	}, nil
}

// resolveInstallScope picks the scope from the request, prompter, or
// non-interactive default (in that order).
func resolveInstallScope(req InstallRequest) (Scope, error) {
	if req.Scope != nil {
		return *req.Scope, nil
	}
	if req.Prompter != nil {
		s, err := req.Prompter.AskScope(req.ProjectDir, req.UserDir)
		if err != nil {
			return 0, err
		}
		return s, nil
	}
	return ScopeProject, nil
}

// pickRootDir returns the appropriate root directory for the resolved scope,
// drawn from the request's ProjectDir/UserDir fields.
func pickRootDir(scope Scope, projectDir, userDir string) (string, error) {
	switch scope {
	case ScopeProject:
		if projectDir == "" {
			return "", errors.New("ProjectDir is required for ScopeProject")
		}
		return projectDir, nil
	case ScopeUser:
		if userDir == "" {
			return "", errors.New("UserDir is required for ScopeUser")
		}
		return userDir, nil
	}
	return "", fmt.Errorf("unknown scope: %d", int(scope))
}

// resolveTargets turns explicit target IDs (if any) or detection-plus-
// prompt into a concrete []AgentDir slice. The fallbacks mirror the cobra
// command's previous behaviour:
//   - Explicit IDs: looked up via LookupAgentDir.
//   - With Prompter: detect existing dirs, present multi-select pre-checked
//     with the detected set (or DefaultAgentDirID if none exist).
//   - Without Prompter: install into every detected dir, falling back to
//     DefaultAgentDirID when nothing exists yet.
func resolveTargets(ids []string, scope Scope, rootDir string, prompter Prompter) ([]AgentDir, error) {
	if len(ids) > 0 {
		var out []AgentDir
		for _, id := range ids {
			t := LookupAgentDir(id)
			if t == nil {
				return nil, fmt.Errorf("unknown target %q", id)
			}
			out = append(out, *t)
		}
		return out, nil
	}

	detected := DetectExistingTargets(rootDir, scope)

	if prompter != nil {
		groups := GroupAgentDirsForScope(scope)
		defaults := detected
		if len(defaults) == 0 {
			defaults = []string{DefaultAgentDirID}
		}
		return prompter.AskTargets(scope, groups, defaults)
	}

	// Non-interactive: every detected dir, or the default if none exist.
	if len(detected) > 0 {
		var out []AgentDir
		for _, id := range detected {
			if t := LookupAgentDir(id); t != nil {
				out = append(out, *t)
			}
		}
		return out, nil
	}
	d := LookupAgentDir(DefaultAgentDirID)
	if d == nil {
		return nil, errors.New("default agent dir not found in registry")
	}
	return []AgentDir{*d}, nil
}
