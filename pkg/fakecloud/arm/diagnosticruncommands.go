// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package arm

import (
	"net/http"
	"regexp"
)

// A virtual machine's diagnostic run commands
// (Microsoft.Compute/virtualMachines/*/diagnosticRunCommands, 2026-04-01;
// docs/plan.md f55), as far as the fake models them: the run command's
// source is one of an inline script, a script URI, a command ID or a
// gallery script version (400), and a gallery script version exists (404).
// Not modelled: the script running, its output and error blobs, the
// account it runs as, and the error codes. (A VM's ordinary run commands,
// which predate this rule, are not checked.)
var diagnosticRunCommand = regexp.MustCompile(`(?i)/providers/microsoft\.compute/virtualmachines/[^/]+/diagnosticruncommands/[^/]+$`)

// checkDiagnosticRunCommand refuses what the rule above does not allow;
// code is "" when it is allowed or id is not a diagnostic run command.
func (a *ARM) checkDiagnosticRunCommand(id string, body map[string]any) (status int, code, message string) {
	if !diagnosticRunCommand.MatchString(id) {
		return 0, "", ""
	}
	props, _ := body["properties"].(map[string]any)
	source, _ := props["source"].(map[string]any)
	n := 0
	for _, k := range []string{"script", "scriptUri", "commandId", "galleryScriptReferenceId"} {
		if s, _ := source[k].(string); s != "" {
			n++
		}
	}
	if n != 1 {
		return http.StatusBadRequest, "InvalidRunCommandSource", "A run command's source is one of script, scriptUri, commandId or galleryScriptReferenceId."
	}
	if ref, _ := source["galleryScriptReferenceId"].(string); ref != "" && !a.exists(ref) {
		return http.StatusNotFound, "ResourceNotFound", "The gallery script version " + ref + " was not found."
	}
	return 0, "", ""
}
