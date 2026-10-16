package orchestrator

import (
	"fmt"
	"strings"

	"github.com/e11en/agentgopher/agent"
	"github.com/e11en/agentgopher/sandbox"
)

// AssertResumeSessionExists fails fast when the session to resume is not on
// the host. With no sandbox the agent wrote the session in place under a
// cwd-derived directory we cannot reliably reconstruct, so it is located by
// id; with a sandbox, capture put it at the host-cwd-derived path.
func AssertResumeSessionExists(provider agent.Provider, kind sandbox.Kind, hostRepoDir, sessionID string) error {
	// Session ids become file names on both sides; a separator would walk out of the session dir.
	if strings.ContainsAny(sessionID, `/\`) || sessionID == "" {
		return fmt.Errorf("ResumeSession %q is not a valid session id", sessionID)
	}
	sp, ok := provider.(agent.SessionStorageProvider)
	if !ok || sp.SessionStorage() == nil {
		return fmt.Errorf("%s does not support ResumeSession", provider.Name())
	}
	st := sp.SessionStorage()
	if kind == sandbox.KindNone {
		found, err := st.FindByIDOnHost(sessionID)
		if err != nil {
			return err
		}
		if found.Path == "" {
			return fmt.Errorf("ResumeSession %q not found under %s", sessionID, found.SearchedRoot)
		}
		return nil
	}
	exists, err := st.ExistsOnHost(hostRepoDir, sessionID)
	if err != nil {
		return err
	}
	if !exists {
		if p := st.HostSessionFilePath(hostRepoDir, sessionID); p != "" {
			return fmt.Errorf("ResumeSession %q not found: expected session file at %s", sessionID, p)
		}
		return fmt.Errorf("ResumeSession %q not found", sessionID)
	}
	return nil
}
