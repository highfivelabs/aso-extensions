// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package arm

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
)

// Recovery Services backup of virtual machines
// (Microsoft.RecoveryServices/vaults/backupFabrics/protectionContainers/protectedItems,
// API 2026-07-01), as far as the fake models it, after the spec
// (specification/recoveryservicesbackup, stable/2026-07-01, bms.json and its
// examples) and Azure's REST guide for backing up a VM:
//
//   - The fabric segment is no resource: backupFabrics/Azure exists under
//     every vault; another fabric name is refused.
//   - Discovery: POST .../backupFabrics/Azure/refreshContainers makes the
//     vault see the virtual machines its subscription holds at that moment.
//     It is long-running (202 with a Location that answers 202 once, then
//     204). A protected item for a machine the vault has not discovered is
//     refused with 404, so a reconciler that refreshes and retries gets past
//     it.
//   - PUT of a protected item for a virtual machine (protectedItemType
//     Microsoft.Compute/virtualMachines): the machine (sourceResourceId)
//     must exist; the container and item names must be the machine's
//     (iaasvmcontainer;iaasvmcontainerv2;<rg>;<vm>, or without the leading
//     iaasvmcontainer;, and vm;iaasvmcontainerv2;<rg>;<vm>); the policy
//     (policyId) must be a backup policy of the same vault, and is required
//     unless protectionState is ProtectionStopped; protectionState may be
//     Protected, BackupsSuspended or ProtectionStopped; a machine protected
//     by another vault is refused. Azure registers the machine's protection
//     container itself; so does the fake. The answer is 202 with an
//     Azure-AsyncOperation (operationsStatus: InProgress once, then
//     Succeeded) and a Location; the item is stored at once, with what Azure
//     computes (containerName, friendlyName, backupManagementType AzureIaasVM,
//     workloadType VM, protectionStatus Healthy, healthStatus Passed, and
//     protectionState IRPending until a first backup when protection is on).
//   - DELETE of a protected item is long-running too (202, the vault's
//     backupOperations); the container goes with the machine's last item.
//   - A vault that holds protected items cannot be deleted.
//
// Not modelled: backups themselves (no recovery points, lastBackupTime, no
// state ever leaves IRPending), soft delete (Azure keeps a deleted item's
// data, soft-deleted, for the vault's retention days, and the vault cannot
// be deleted until it is gone), roles (Azure needs Backup Contributor and
// rights on the machine), the machine's region (Azure requires the vault in
// the machine's region), item types other than virtual machines (file
// shares, SQL and SAP HANA in VMs), and Azure's exact error codes for these
// refusals (the codes are the fake's).
var (
	backupItem          = regexp.MustCompile(`(?i)^(/subscriptions/[^/]+/resourcegroups/[^/]+/providers/microsoft\.recoveryservices/vaults/[^/]+)/backupfabrics/([^/]+)/protectioncontainers/([^/]+)/protecteditems/([^/]+)$`)
	backupRefresh       = regexp.MustCompile(`(?i)^(/subscriptions/([^/]+)/resourcegroups/[^/]+/providers/microsoft\.recoveryservices/vaults/[^/]+)/backupfabrics/([^/]+)/refreshcontainers$`)
	backupOperation     = regexp.MustCompile(`(?i)^/subscriptions/[^/]+/resourcegroups/[^/]+/providers/microsoft\.recoveryservices/vaults/[^/]+(?:/.*)?/(operationsstatus|operationresults|backupoperations|backupoperationresults)/([^/]+)$`)
	recoveryVault       = regexp.MustCompile(`(?i)^/subscriptions/[^/]+/resourcegroups/[^/]+/providers/microsoft\.recoveryservices/vaults/[^/]+$`)
	virtualMachine      = regexp.MustCompile(`(?i)^/subscriptions/([^/]+)/resourcegroups/([^/]+)/providers/microsoft\.compute/virtualmachines/([^/]+)$`)
	backupPolicyOfVault = regexp.MustCompile(`(?i)^(.*/providers/microsoft\.recoveryservices/vaults/[^/]+)/backuppolicies/[^/]+$`)
)

const protectedItemType = "Microsoft.Compute/virtualMachines"

// serveBackup answers what the fake models of virtual machine backup; it
// reports whether path was one of those requests.
func (a *ARM) serveBackup(w http.ResponseWriter, r *http.Request, path string) bool {
	switch {
	case backupRefresh.MatchString(path) && r.Method == http.MethodPost:
		a.serveRefresh(w, r, backupRefresh.FindStringSubmatch(path))
	case backupOperation.MatchString(path) && r.Method == http.MethodGet:
		a.serveBackupOperation(w, backupOperation.FindStringSubmatch(path))
	case backupItem.MatchString(path):
		m := backupItem.FindStringSubmatch(path)
		switch r.Method {
		case http.MethodPut:
			a.putProtectedItem(w, r, path, m)
		case http.MethodDelete:
			a.deleteProtectedItem(w, r, path, m)
		default:
			return false // GET: the store
		}
	case recoveryVault.MatchString(path) && r.Method == http.MethodDelete:
		if item := a.protectedItemIn(path); item != "" {
			writeError(w, http.StatusBadRequest, "ServiceResourceNotEmpty", "The vault "+path+" cannot be deleted as there are existing resources within the vault: "+item+". Stop protection and delete its backup items first.")
			return true
		}
		return false
	default:
		return false
	}
	return true
}

// newBackupOperation starts an operation that is in progress for one poll.
func (a *ARM) newBackupOperation() string {
	if a.backupOps == nil {
		a.backupOps = map[string]int{}
	}
	a.opSeq++
	id := fmt.Sprintf("00000000-0000-0000-0000-%012d", a.opSeq)
	a.backupOps[id] = 1
	return id
}

func baseURL(r *http.Request) string {
	scheme := "https"
	if r.TLS == nil {
		scheme = "http"
	}
	return scheme + "://" + r.Host
}

func (a *ARM) serveRefresh(w http.ResponseWriter, r *http.Request, m []string) {
	vault, subscription, fabricName := m[1], m[2], m[3]
	if !a.exists(vault) {
		writeError(w, http.StatusNotFound, "ResourceNotFound", "The vault "+vault+" was not found.")
		return
	}
	if !strings.EqualFold(fabricName, "Azure") {
		writeError(w, http.StatusBadRequest, "BMSUserErrorInvalidInput", "The fabric "+fabricName+" is not Azure.")
		return
	}
	if a.discovered == nil {
		a.discovered = map[string]map[string]bool{}
	}
	seen := map[string]bool{}
	for k := range a.resources {
		if vm := virtualMachine.FindStringSubmatch(k); vm != nil && strings.EqualFold(vm[1], subscription) {
			seen[k] = true
		}
	}
	a.discovered[strings.ToLower(vault)] = seen
	op := a.newBackupOperation()
	w.Header().Set("Location", baseURL(r)+vault+"/backupFabrics/Azure/operationResults/"+op+"?api-version="+r.URL.Query().Get("api-version"))
	w.Header().Set("Retry-After", "1")
	w.WriteHeader(http.StatusAccepted)
}

// serveBackupOperation answers an operation's status (operationsStatus,
// backupOperations: a JSON status) or result (operationResults,
// backupOperationResults: 202 until done, then 204).
func (a *ARM) serveBackupOperation(w http.ResponseWriter, m []string) {
	kind, id := strings.ToLower(m[1]), m[2]
	left, ok := a.backupOps[id]
	if !ok {
		writeError(w, http.StatusNotFound, "OperationNotFound", "The operation "+id+" was not found.")
		return
	}
	if left > 0 {
		a.backupOps[id] = left - 1
	}
	done := left == 0
	switch kind {
	case "operationsstatus", "backupoperations":
		status := "InProgress"
		if done {
			status = "Succeeded"
		}
		writeJSON(w, http.StatusOK, map[string]any{"id": id, "name": id, "status": status, "startTime": "2026-01-01T00:00:00Z"})
	default:
		if !done {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func (a *ARM) putProtectedItem(w http.ResponseWriter, r *http.Request, id string, m []string) {
	vault, fabricName, container, name := m[1], m[2], m[3], m[4]
	if !a.exists(vault) {
		writeError(w, http.StatusNotFound, "ParentResourceNotFound", "The vault "+vault+" was not found.")
		return
	}
	if !strings.EqualFold(fabricName, "Azure") {
		writeError(w, http.StatusBadRequest, "BMSUserErrorInvalidInput", "The fabric "+fabricName+" is not Azure.")
		return
	}
	body := map[string]any{}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "InvalidRequestContent", err.Error())
		return
	}
	props, _ := body["properties"].(map[string]any)
	if props == nil {
		writeError(w, http.StatusBadRequest, "BMSUserErrorInvalidInput", "A protected item needs properties.")
		return
	}
	if t, _ := props["protectedItemType"].(string); !strings.EqualFold(t, protectedItemType) {
		writeError(w, http.StatusBadRequest, "BMSUserErrorInvalidInput", "The fake models protected virtual machines only (protectedItemType "+protectedItemType+"), not "+t+".")
		return
	}
	source, _ := props["sourceResourceId"].(string)
	vm := virtualMachine.FindStringSubmatch(source)
	if vm == nil {
		writeError(w, http.StatusBadRequest, "BMSUserErrorInvalidInput", "sourceResourceId "+source+" is not a virtual machine.")
		return
	}
	if !a.exists(source) {
		writeError(w, http.StatusNotFound, "ResourceNotFound", "The virtual machine "+source+" was not found.")
		return
	}
	if !a.discovered[strings.ToLower(vault)][strings.ToLower(source)] {
		writeError(w, http.StatusNotFound, "BMSUserErrorContainerObjectNotFound", "The vault has not discovered the virtual machine "+source+"; refresh its containers (POST backupFabrics/Azure/refreshContainers) first.")
		return
	}
	suffix := strings.ToLower("iaasvmcontainerv2;" + vm[2] + ";" + vm[3])
	if c := strings.ToLower(container); c != suffix && c != "iaasvmcontainer;"+suffix {
		writeError(w, http.StatusBadRequest, "BMSUserErrorInvalidInput", "The container of "+source+" is iaasvmcontainer;"+suffix+", not "+container+".")
		return
	}
	if !strings.EqualFold(name, "vm;"+suffix) {
		writeError(w, http.StatusBadRequest, "BMSUserErrorInvalidInput", "The protected item of "+source+" is vm;"+suffix+", not "+name+".")
		return
	}
	state, _ := props["protectionState"].(string)
	switch state {
	case "", "Protected", "BackupsSuspended", "ProtectionStopped":
	default:
		writeError(w, http.StatusBadRequest, "BMSUserErrorInvalidInput", "protectionState "+state+" cannot be set; use Protected, BackupsSuspended or ProtectionStopped.")
		return
	}
	policy, _ := props["policyId"].(string)
	if policy == "" && state != "ProtectionStopped" {
		writeError(w, http.StatusBadRequest, "BMSUserErrorPolicyNotFound", "policyId is required to protect a virtual machine.")
		return
	}
	if policy != "" {
		pm := backupPolicyOfVault.FindStringSubmatch(policy)
		if pm == nil || !strings.EqualFold(pm[1], vault) {
			writeError(w, http.StatusBadRequest, "BMSUserErrorInvalidPolicyInput", "policyId "+policy+" is not a backup policy of the vault "+vault+".")
			return
		}
		if !a.exists(policy) {
			writeError(w, http.StatusNotFound, "BMSUserErrorPolicyNotFound", "The backup policy "+policy+" was not found.")
			return
		}
	}
	for k, res := range a.resources {
		if m := backupItem.FindStringSubmatch(k); m != nil && !strings.EqualFold(m[1], vault) {
			p, _ := (*res)["properties"].(map[string]any)
			if s, _ := p["sourceResourceId"].(string); strings.EqualFold(s, source) {
				writeError(w, http.StatusBadRequest, "UserErrorVmAlreadyProtected", "The virtual machine "+source+" is already protected by the vault "+m[1]+".")
				return
			}
		}
	}

	containerID := vault + "/backupFabrics/Azure/protectionContainers/" + container
	ck := strings.ToLower(containerID)
	if _, ok := a.resources[ck]; !ok {
		c := resource{"id": containerID, "name": container, "type": typeOf(containerID), "properties": map[string]any{
			"friendlyName": vm[3], "backupManagementType": "AzureIaasVM", "registrationStatus": "Registered",
			"healthStatus": "Healthy", "containerType": protectedItemType, "virtualMachineId": source,
		}}
		a.setSystemData(ck, c)
		a.resources[ck] = &c
	}
	key := strings.ToLower(id)
	if state == "" || state == "Protected" {
		state = "IRPending" // until a first backup, which the fake never takes
	}
	props["protectionState"] = state
	props["sourceResourceId"] = source
	props["virtualMachineId"] = source
	props["containerName"] = suffix
	props["friendlyName"] = vm[3]
	props["backupManagementType"] = "AzureIaasVM"
	props["workloadType"] = "VM"
	props["protectionStatus"] = "Healthy"
	props["healthStatus"] = "Passed"
	item := resource{"id": id, "name": name, "type": typeOf(id), "properties": props}
	a.setSystemData(key, item)
	a.resources[key] = &item

	op := a.newBackupOperation()
	base := baseURL(r) + id
	q := "?api-version=" + r.URL.Query().Get("api-version")
	w.Header().Set("Azure-AsyncOperation", base+"/operationsStatus/"+op+q)
	w.Header().Set("Location", base+"/operationResults/"+op+q)
	w.Header().Set("Retry-After", "1")
	w.WriteHeader(http.StatusAccepted)
}

func (a *ARM) deleteProtectedItem(w http.ResponseWriter, r *http.Request, id string, m []string) {
	key := strings.ToLower(id)
	if _, ok := a.resources[key]; !ok {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if l := a.lockOver(id, ""); l != nil {
		writeScopeLocked(w, r.Method, id, l)
		return
	}
	delete(a.resources, key)
	container := strings.ToLower(m[1] + "/backupFabrics/" + m[2] + "/protectionContainers/" + m[3])
	last := true
	for k := range a.resources {
		if strings.HasPrefix(k, container+"/protecteditems/") {
			last = false
		}
	}
	if last {
		delete(a.resources, container)
	}
	op := a.newBackupOperation()
	q := "?api-version=" + r.URL.Query().Get("api-version")
	w.Header().Set("Azure-AsyncOperation", baseURL(r)+m[1]+"/backupOperations/"+op+q)
	w.Header().Set("Location", baseURL(r)+m[1]+"/backupOperationResults/"+op+q)
	w.Header().Set("Retry-After", "1")
	w.WriteHeader(http.StatusAccepted)
}

// protectedItemIn is the ID of a protected item the vault holds, or "".
func (a *ARM) protectedItemIn(vault string) string {
	prefix := strings.ToLower(vault) + "/backupfabrics/"
	for k, res := range a.resources {
		if strings.HasPrefix(k, prefix) && backupItem.MatchString(k) {
			id, _ := (*res)["id"].(string)
			return id
		}
	}
	return ""
}
