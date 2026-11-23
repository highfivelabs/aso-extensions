// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package arm

import (
	"net/http"
	"regexp"
	"strings"
)

// DevTest Labs (Microsoft.DevTestLab/labs/*, 2018-09-15; docs/plan.md f51),
// as far as the fake models them. Each rule is one Azure's DevTest Labs
// documentation states:
//
//   - a lab's own schedules have fixed names: LabVmsShutdown runs the
//     LabVmsShutdownTask and LabVmAutoStart the LabVmsStartupTask (400);
//     a lab VM's auto-shutdown is LabVmsShutdown with LabVmsShutdownTask
//     (400);
//   - a lab's cost settings are named targetCost (400);
//   - a lab virtual network names a virtual network that exists (404);
//   - a lab VM names a virtual network of its own lab that exists (404) and
//     one of that network's lab subnets (400), and signs in with a password
//     or, when isAuthenticationWithSshKey, an SSH key (400);
//   - a user's secret has a value (400).
//
// Not modelled: VMs, disks and environments being made in the lab's
// resource group, artifacts being applied, schedules running, the lab's
// policies, and the error codes (the fake's own).
var (
	dtlLab           = `(?i)^(.*/providers/microsoft\.devtestlab/labs/[^/]+)`
	dtlLabSchedule   = regexp.MustCompile(dtlLab + `/schedules/([^/]+)$`)
	dtlVMSchedule    = regexp.MustCompile(dtlLab + `/virtualmachines/[^/]+/schedules/([^/]+)$`)
	dtlCost          = regexp.MustCompile(dtlLab + `/costs/([^/]+)$`)
	dtlVirtualNet    = regexp.MustCompile(dtlLab + `/virtualnetworks/[^/]+$`)
	dtlVirtualMach   = regexp.MustCompile(dtlLab + `/virtualmachines/[^/]+$`)
	dtlUserSecret    = regexp.MustCompile(dtlLab + `/users/[^/]+/secrets/[^/]+$`)
	dtlLabScheduleOK = map[string]string{"labvmsshutdown": "LabVmsShutdownTask", "labvmautostart": "LabVmsStartupTask"}
)

// checkDevTestLab refuses what the rules above do not allow; code is "" when
// it is allowed or id is none of these.
func (a *ARM) checkDevTestLab(id string, body map[string]any) (status int, code, message string) {
	props, _ := body["properties"].(map[string]any)
	str := func(k string) string { s, _ := props[k].(string); return s }
	switch {
	case dtlVMSchedule.MatchString(id):
		name := dtlVMSchedule.FindStringSubmatch(id)[2]
		if !strings.EqualFold(name, "LabVmsShutdown") || !strings.EqualFold(str("taskType"), "LabVmsShutdownTask") {
			return http.StatusBadRequest, "InvalidScheduleName", "A lab VM's schedule is LabVmsShutdown, with the LabVmsShutdownTask task."
		}
	case dtlLabSchedule.MatchString(id):
		name := dtlLabSchedule.FindStringSubmatch(id)[2]
		task, ok := dtlLabScheduleOK[strings.ToLower(name)]
		if !ok || !strings.EqualFold(str("taskType"), task) {
			return http.StatusBadRequest, "InvalidScheduleName", "A lab's schedules are LabVmsShutdown (LabVmsShutdownTask) and LabVmAutoStart (LabVmsStartupTask)."
		}
	case dtlCost.MatchString(id):
		if name := dtlCost.FindStringSubmatch(id)[2]; !strings.EqualFold(name, "targetCost") {
			return http.StatusBadRequest, "InvalidCostName", "A lab's cost settings are named targetCost, not " + name + "."
		}
	case dtlVirtualNet.MatchString(id):
		if vnet := str("externalProviderResourceId"); vnet != "" && !a.exists(vnet) {
			return http.StatusNotFound, "ResourceNotFound", "The virtual network " + vnet + " was not found."
		}
	case dtlVirtualMach.MatchString(id):
		lab := strings.ToLower(dtlVirtualMach.FindStringSubmatch(id)[1])
		vnet := str("labVirtualNetworkId")
		if vnet != "" {
			if !strings.HasPrefix(strings.ToLower(vnet), lab+"/virtualnetworks/") || !a.exists(vnet) {
				return http.StatusNotFound, "ResourceNotFound", "The lab virtual network " + vnet + " was not found in the lab."
			}
			if subnet := str("labSubnetName"); subnet != "" && !a.labSubnet(vnet, subnet) {
				return http.StatusBadRequest, "InvalidLabSubnet", "The lab virtual network has no lab subnet " + subnet + "."
			}
		}
		if ssh, _ := props["isAuthenticationWithSshKey"].(bool); ssh {
			if str("sshKey") == "" {
				return http.StatusBadRequest, "SshKeyRequired", "A VM that signs in with an SSH key needs the key (sshKey)."
			}
		} else if str("password") == "" {
			return http.StatusBadRequest, "PasswordRequired", "A VM that signs in with a password needs it (password)."
		}
	case dtlUserSecret.MatchString(id):
		if str("value") == "" {
			return http.StatusBadRequest, "SecretValueRequired", "A secret needs its value."
		}
	}
	return 0, "", ""
}

// labSubnet reports whether the lab virtual network vnet has a subnet
// override named subnet.
func (a *ARM) labSubnet(vnet, subnet string) bool {
	res, ok := a.resources[strings.ToLower(vnet)]
	if !ok {
		return false
	}
	props, _ := (*res)["properties"].(map[string]any)
	overrides, _ := props["subnetOverrides"].([]any)
	for _, o := range overrides {
		m, _ := o.(map[string]any)
		if name, _ := m["labSubnetName"].(string); strings.EqualFold(name, subnet) {
			return true
		}
	}
	return false
}
