// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

//go:build live

package live

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"

	asocompute "github.com/Azure/azure-service-operator/v2/api/compute/v20220301/storage"

	devtestlab "github.com/highfivelabs/aso-extensions/apis/arm/devtestlab/v20180915"
	maintenance "github.com/highfivelabs/aso-extensions/apis/arm/maintenance/v20230401"
	rs "github.com/highfivelabs/aso-extensions/apis/arm/recoveryservices/v20260701"
)

const (
	rsAPI          = "2024-04-01"
	maintenanceAPI = "2023-04-01"
	devTestLabAPI  = "2018-09-15"
)

// A Recovery Services vault (free with nothing protected), a VM backup
// policy in it, the vault's private endpoint connection approved by our
// kind (the endpoint a fixture, about $0.01 an hour), and a resource guard
// proxy to a resource guard fixture (free).
func TestLiveRecoveryServices(t *testing.T) {
	t.Parallel()
	requireProvider(t, "Microsoft.RecoveryServices")
	ns := namespace(t, "rsv")
	vault := &rs.Vault{}
	vault.Namespace, vault.Name = ns, "vault"
	vault.Spec = rs.Vault_Spec{
		AzureName: azName("rsv"), Owner: owner("rg"), Location: ptr(location),
		Sku:      &rs.Sku{Name: ptr(rs.SkuName_RS0), Tier: ptr("Standard")},
		Identity: &rs.IdentityData{Type: ptr(rs.ResourceIdentityType("SystemAssigned"))},
		Properties: &rs.VaultProperties{
			PublicNetworkAccess: ptr(rs.PublicNetworkAccess("Enabled")),
			RedundancySettings: &rs.VaultPropertiesRedundancySettings{
				StandardTierStorageRedundancy: ptr(rs.StandardTierStorageRedundancy_LocallyRedundant),
				CrossRegionRestore:            ptr(rs.CrossRegionRestore("Disabled")),
			},
		},
	}
	policy := &rs.VaultsBackupPolicy{}
	policy.Namespace, policy.Name = ns, "daily"
	policy.Spec = rs.VaultsBackupPolicy_Spec{
		AzureName: "daily-vm", Owner: owner("vault"), Location: ptr(location),
		Properties: &rs.ProtectionPolicy{AzureIaasVM: &rs.AzureIaaSVMProtectionPolicy{
			BackupManagementType: ptr(rs.AzureIaaSVMProtectionPolicy_BackupManagementType_AzureIaasVM),
			PolicyType:           ptr(rs.IAASVMPolicyType_V1),
			TimeZone:             ptr("UTC"),
			SchedulePolicy: &rs.SchedulePolicy{SimpleSchedulePolicy: &rs.SimpleSchedulePolicy{
				SchedulePolicyType:   ptr(rs.SimpleSchedulePolicy_SchedulePolicyType("SimpleSchedulePolicy")),
				ScheduleRunFrequency: ptr(rs.ScheduleRunType_Daily),
				ScheduleRunTimes:     []string{"2026-01-01T02:00:00Z"},
			}},
			RetentionPolicy: &rs.RetentionPolicy{LongTerm: &rs.LongTermRetentionPolicy{
				RetentionPolicyType: ptr(rs.LongTermRetentionPolicy_RetentionPolicyType_LongTermRetentionPolicy),
				DailySchedule: &rs.DailyRetentionSchedule{
					RetentionTimes:    []string{"2026-01-01T02:00:00Z"},
					RetentionDuration: &rs.RetentionDuration{Count: ptr(30), DurationType: ptr(rs.RetentionDurationType_Days)},
				},
			}},
		}},
	}
	create(t, timeout, vault, policy)
	vaultID := rgID() + "/providers/Microsoft.RecoveryServices/vaults/" + vault.Spec.AzureName
	policyID := vaultID + "/backupPolicies/daily-vm"
	expectResource(t, vaultID, rsAPI, func(r map[string]any) error {
		sku, _ := r["sku"].(map[string]any)
		id, _ := r["identity"].(map[string]any)
		if sku["name"] != "RS0" || id["type"] != "SystemAssigned" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	retention := func(want float64) func(map[string]any) error {
		return func(r map[string]any) error {
			ret, _ := properties(r)["retentionPolicy"].(map[string]any)
			daily, _ := ret["dailySchedule"].(map[string]any)
			dur, _ := daily["retentionDuration"].(map[string]any)
			if properties(r)["backupManagementType"] != "AzureIaasVM" || dur["count"] != want {
				return fmt.Errorf("resource: %v", r)
			}
			return nil
		}
	}
	expectResource(t, policyID, rsAPI, retention(30))
	update(t, policy, func(p *rs.VaultsBackupPolicy) {
		p.Spec.Properties.AzureIaasVM.RetentionPolicy.LongTerm.DailySchedule.RetentionDuration.Count = ptr(60)
	})
	eventually(t, timeout, "VaultsBackupPolicy Ready after the change", ready(policy))
	expectResource(t, policyID, rsAPI, retention(60))

	t.Run("PrivateEndpointConnection", func(t *testing.T) {
		peID := privateEndpoint(t, "rsvpe", "10.82.0.0/16", vaultID, "AzureBackup")
		connName := pendingConnection(t, vaultID, rsAPI, peID)
		conn := &rs.VaultsPrivateEndpointConnection{}
		conn.Namespace, conn.Name = ns, "pe"
		conn.Spec = rs.VaultsPrivateEndpointConnection_Spec{
			AzureName: connName, Owner: owner("vault"), Location: ptr(location),
			Properties: &rs.BmsPrivateEndpointConnection{
				PrivateEndpoint: &rs.BmsPrivateEndpoint{Reference: &genruntime.ResourceReference{ARMID: peID}},
				GroupIds:        []rs.VaultSubResourceType{rs.VaultSubResourceType_AzureBackup},
				PrivateLinkServiceConnectionState: &rs.BmsPrivateLinkServiceConnectionState{
					Status: ptr(rs.PrivateEndpointConnectionStatus_Approved), Description: ptr("approved by the live test"),
				},
			},
		}
		create(t, timeout, conn)
		connID := vaultID + "/privateEndpointConnections/" + connName
		expectResource(t, connID, rsAPI, func(r map[string]any) error {
			st, _ := properties(r)["privateLinkServiceConnectionState"].(map[string]any)
			if st["status"] != "Approved" {
				return fmt.Errorf("resource: %v", r)
			}
			return nil
		})
		remove(t, timeout, conn)
		expectGone(t, connID, rsAPI)
	})

	t.Run("ResourceGuardProxy", func(t *testing.T) {
		requireProvider(t, "Microsoft.DataProtection")
		guardID := rgID() + "/providers/Microsoft.DataProtection/resourceGuards/" + azName("guard")
		fixture(t, guardID, "2023-05-01", map[string]any{"location": location, "properties": map[string]any{}})
		proxy := &rs.VaultsBackupResourceGuardProxy{}
		proxy.Namespace, proxy.Name = ns, "guard"
		proxy.Spec = rs.VaultsBackupResourceGuardProxy_Spec{
			AzureName: "VaultProxy", Owner: owner("vault"),
			Properties: &rs.ResourceGuardProxyBase{
				ResourceGuardResourceReference: &genruntime.ResourceReference{ARMID: guardID},
				Description:                    ptr("multi-user authorization for the live test"),
			},
		}
		if err := k8s.Create(context.Background(), proxy); err != nil {
			t.Fatal(err)
		}
		proxyID := vaultID + "/backupResourceGuardProxies/VaultProxy"
		eventually(t, timeout, "VaultsBackupResourceGuardProxy Ready", ready(proxy))
		expectResource(t, proxyID, rsAPI, func(r map[string]any) error {
			if !strings.EqualFold(fmt.Sprint(properties(r)["resourceGuardResourceId"]), guardID) {
				return fmt.Errorf("resource: %v", r)
			}
			return nil
		})
		// Removing the proxy turns multi-user authorization off, an operation
		// the guard protects: Azure wants an unlockDelete first. If our kind
		// does not get the delete through on its own, say so, then unlock it
		// as an operator would and let the kind finish.
		if err := k8s.Delete(context.Background(), proxy); err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(4 * time.Minute)
		for time.Now().Before(deadline) && gone(proxy)() != nil {
			time.Sleep(poll)
		}
		if gone(proxy)() != nil {
			_ = k8s.Get(context.Background(), client.ObjectKeyFromObject(proxy), proxy)
			t.Errorf("the proxy was not deleted on its own: %v", proxy.Status.Conditions)
			if _, _, err := azure.do(context.Background(), http.MethodPost, proxyID+"/unlockDelete", rsAPI, map[string]any{
				"resourceGuardOperationRequests": []string{guardID + "/deleteResourceGuardProxyRequests/default"},
				"resourceToBeDeleted":            proxyID,
			}); err != nil {
				t.Errorf("unlockDelete: %v", err)
			}
			eventually(t, timeout, "the proxy gone after unlockDelete", gone(proxy))
		}
		expectGone(t, proxyID, rsAPI)
	})

	remove(t, timeout, policy)
	expectGone(t, policyID, rsAPI)
	remove(t, timeout, vault)
	expectGone(t, vaultID, rsAPI)
}

// A maintenance configuration (free) assigned to a VM fixture (two vCPUs,
// about $0.03 an hour) and, with a filter, to the resource group; and the
// VM's DevTest Labs auto-shutdown schedule (free).
func TestLiveMaintenanceAndShutdown(t *testing.T) {
	t.Parallel()
	requireProvider(t, "Microsoft.Maintenance")
	requireProvider(t, "Microsoft.DevTestLab")
	ns := namespace(t, "maint")
	vmID, vmName := linuxVM(t, "maintvm", "10.83.0.0/16")
	vm := &asocompute.VirtualMachine{}
	vm.Namespace, vm.Name = ns, "vm"
	vm.Spec.AzureName, vm.Spec.Owner = vmName, owner("rg")
	standIn(t, vm, vmID, nil)

	start := time.Now().UTC().AddDate(0, 0, 14).Format("2006-01-02") + " 02:00"
	window := &maintenance.MaintenanceConfiguration{}
	window.Namespace, window.Name = ns, "patching"
	window.Spec = maintenance.MaintenanceConfiguration_Spec{
		AzureName: azName("patching"), Owner: owner("rg"), Location: ptr(location),
		MaintenanceScope:    ptr(maintenance.MaintenanceConfigurationProperties_MaintenanceScope_InGuestPatch),
		ExtensionProperties: map[string]string{"InGuestPatchMode": "User"},
		InstallPatches: &maintenance.InputPatchConfiguration{
			RebootSetting:   ptr(maintenance.InputPatchConfiguration_RebootSetting("IfRequired")),
			LinuxParameters: &maintenance.InputLinuxParameters{ClassificationsToInclude: []string{"Critical", "Security"}},
		},
		MaintenanceWindow: &maintenance.MaintenanceWindow{
			StartDateTime: ptr(start), Duration: ptr("03:00"), TimeZone: ptr("UTC"), RecurEvery: ptr("Week Saturday"),
		},
	}
	configRef := &genruntime.ResourceReference{Group: "maintenance.azure.hfl.dev", Kind: "MaintenanceConfiguration", Name: "patching"}
	onVM := &maintenance.ConfigurationAssignment{}
	onVM.Namespace, onVM.Name = ns, "vm-patching"
	onVM.Spec = maintenance.ConfigurationAssignment_Spec{
		AzureName: "patching", Owner: &genruntime.ArbitraryOwnerReference{Group: "compute.azure.com", Kind: "VirtualMachine", Name: "vm"},
		Location: ptr(location), MaintenanceConfigurationReference: configRef,
	}
	dynamic := &maintenance.ConfigurationAssignment{}
	dynamic.Namespace, dynamic.Name = ns, "weekly"
	dynamic.Spec = maintenance.ConfigurationAssignment_Spec{
		AzureName: azName("weekly"), Owner: rgRef(), Location: ptr(location), MaintenanceConfigurationReference: configRef,
		Filter: &maintenance.ConfigurationAssignmentFilterProperties{
			ResourceTypes: []string{"Microsoft.Compute/virtualMachines"},
			OsTypes:       []string{"Linux"},
			TagSettings: &maintenance.TagSettingsProperties{
				Tags:           map[string][]string{"patch": {"weekly"}},
				FilterOperator: ptr(maintenance.TagSettingsProperties_FilterOperator_Any),
			},
		},
	}
	shutdownName := "shutdown-computevm-" + vmName
	shutdown := &devtestlab.Schedule{}
	shutdown.Namespace, shutdown.Name = ns, "shutdown"
	shutdown.Spec = devtestlab.Schedule_Spec{
		AzureName: shutdownName, Owner: owner("rg"), Location: ptr(location),
		TaskType: ptr("ComputeVmShutdownTask"), Status: ptr(devtestlab.ScheduleProperties_Status_Enabled),
		TimeZoneId: ptr("UTC"), DailyRecurrence: &devtestlab.DayDetails{Time: ptr("1900")},
		TargetResourceReference: &genruntime.ResourceReference{ARMID: vmID},
		NotificationSettings:    &devtestlab.NotificationSettings{Status: ptr(devtestlab.NotificationSettings_Status_Disabled), TimeInMinutes: ptr(30)},
	}
	create(t, timeout, window, onVM, dynamic, shutdown)

	windowID := rgID() + "/providers/Microsoft.Maintenance/maintenanceConfigurations/" + window.Spec.AzureName
	onVMID := vmID + "/providers/Microsoft.Maintenance/configurationAssignments/patching"
	dynamicID := rgID() + "/providers/Microsoft.Maintenance/configurationAssignments/" + dynamic.Spec.AzureName
	shutdownID := rgID() + "/providers/Microsoft.DevTestLab/schedules/" + shutdownName
	expectResource(t, windowID, maintenanceAPI, func(r map[string]any) error {
		w, _ := properties(r)["maintenanceWindow"].(map[string]any)
		if properties(r)["maintenanceScope"] != "InGuestPatch" || w["recurEvery"] != "Week Saturday" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, onVMID, maintenanceAPI, func(r map[string]any) error {
		if !strings.EqualFold(fmt.Sprint(properties(r)["maintenanceConfigurationId"]), windowID) {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	osTypes := func(n int) func(map[string]any) error {
		return func(r map[string]any) error {
			f, _ := properties(r)["filter"].(map[string]any)
			if os, _ := f["osTypes"].([]any); len(os) != n {
				return fmt.Errorf("resource: %v", r)
			}
			return nil
		}
	}
	expectResource(t, dynamicID, maintenanceAPI, osTypes(1))
	expectResource(t, shutdownID, devTestLabAPI, func(r map[string]any) error {
		p := properties(r)
		daily, _ := p["dailyRecurrence"].(map[string]any)
		if p["taskType"] != "ComputeVmShutdownTask" || p["status"] != "Enabled" || daily["time"] != "1900" ||
			!strings.EqualFold(fmt.Sprint(p["targetResourceId"]), vmID) {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	update(t, dynamic, func(a *maintenance.ConfigurationAssignment) { a.Spec.Filter.OsTypes = []string{"Linux", "Windows"} })
	eventually(t, timeout, "the filtered assignment Ready after the change", ready(dynamic))
	expectResource(t, dynamicID, maintenanceAPI, osTypes(2))
	update(t, shutdown, func(s *devtestlab.Schedule) { s.Spec.DailyRecurrence.Time = ptr("2000") })
	eventually(t, timeout, "Schedule Ready after the change", ready(shutdown))
	expectResource(t, shutdownID, devTestLabAPI, func(r map[string]any) error {
		if daily, _ := properties(r)["dailyRecurrence"].(map[string]any); daily["time"] != "2000" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	for _, o := range []conditioned{shutdown, dynamic, onVM, window} {
		remove(t, timeout, o)
	}
	expectGone(t, shutdownID, devTestLabAPI)
	for _, id := range []string{dynamicID, onVMID, windowID} {
		expectGone(t, id, maintenanceAPI)
	}
}
