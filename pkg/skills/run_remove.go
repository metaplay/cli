/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package skills


// RemoveRequest is the high-level input to RunRemove. It mirrors
// InstallRequest's shape so callers that wire up both flows can reuse
// scope/target resolution logic.
type RemoveRequest struct {
	// Scope, when non-nil, forces the removal scope.
	Scope *Scope

	// ProjectDir / UserDir mirror InstallRequest. Tests pass t.TempDir()
	// to keep the real home directory untouched.
	ProjectDir string
	UserDir    string

	// TargetIDs lists AgentDir IDs to consider. Empty: ask Prompter, or
	// in non-interactive mode use detection / DefaultAgentDirID.
	TargetIDs []string

	// SkillIDs optionally restricts removal to specific skill names. When
	// empty, every managed-by wrapper found under the chosen target dirs
	// is removed (covering orphans).
	SkillIDs []string

	// Prompter, if non-nil, drives interactive resolution of unset Scope
	// and empty TargetIDs.
	Prompter Prompter
}

// RemoveResult carries the resolved scope/root/targets alongside the per-
// (skill, target) action records produced by the underlying Remove call.
type RemoveResult struct {
	Scope   Scope
	RootDir string
	Targets []AgentDir
	Actions []RemoveAction
}

// RunRemove is the high-level remove orchestrator: it resolves scope and
// targets (interactively when Prompter is supplied, otherwise from inputs
// and detection), then runs Remove.
func RunRemove(req RemoveRequest) (RemoveResult, error) {
	scope, err := resolveRemoveScope(req)
	if err != nil {
		return RemoveResult{}, err
	}

	rootDir, err := pickRootDir(scope, req.ProjectDir, req.UserDir)
	if err != nil {
		return RemoveResult{}, err
	}

	targets, err := resolveTargets(req.TargetIDs, scope, rootDir, req.Prompter)
	if err != nil {
		return RemoveResult{}, err
	}

	if err := ValidateTargetsForScope(targets, scope); err != nil {
		return RemoveResult{}, err
	}

	actions, err := Remove(RemoveOptions{
		Targets:  targets,
		RootDir:  rootDir,
		Scope:    scope,
		SkillIDs: req.SkillIDs,
	})
	if err != nil {
		return RemoveResult{}, err
	}

	return RemoveResult{
		Scope:   scope,
		RootDir: rootDir,
		Targets: targets,
		Actions: actions,
	}, nil
}

// resolveRemoveScope mirrors resolveInstallScope; kept separate so future
// per-flow tweaks (e.g. different default for remove) don't have to fork
// a shared helper.
func resolveRemoveScope(req RemoveRequest) (Scope, error) {
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

