// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

//go:build live

package live

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"

	citus "github.com/highfivelabs/aso-extensions/apis/arm/dbforpostgresql/v20221108"
)

const cosmosPostgresAPI = "2022-11-08"

// An Azure Cosmos DB for PostgreSQL cluster of one Burstable node (1 vCore,
// 32 GiB; a few cents an hour, about 15 minutes to make), its administrator
// password from a Secret, with a firewall rule, a role (its password from a
// Secret) and a coordinator configuration (no DELETE: detached, and deleted
// with the cluster). Each is made, the firewall rule changed, and all
// deleted (docs/plan.md f46).
func TestLiveCosmosPostgres(t *testing.T) {
	t.Parallel()
	requireProvider(t, "Microsoft.DBforPostgreSQL")
	ns := namespace(t, "citus")
	secret(t, ns, "citus", map[string]string{"admin": password(), "app": password()})

	cluster := &citus.ServerGroupsv2{}
	cluster.Namespace, cluster.Name = ns, "citus"
	cluster.Spec = citus.ServerGroupsv2_Spec{
		AzureName: azName("citus"), Owner: owner("rg"), Location: ptr(location),
		AdministratorLoginPassword:  &genruntime.SecretReference{Name: "citus", Key: "admin"},
		PostgresqlVersion:           ptr("16"),
		CoordinatorServerEdition:    ptr("BurstableMemoryOptimized"),
		CoordinatorVCores:           ptr(1),
		CoordinatorStorageQuotaInMb: ptr(32768),
		NodeCount:                   ptr(0),
		EnableShardsOnCoordinator:   ptr(true),
		EnableHa:                    ptr(false),
	}
	create(t, 45*time.Minute, cluster)
	clusterID := rgID() + "/providers/Microsoft.DBforPostgreSQL/serverGroupsv2/" + cluster.Spec.AzureName
	expectResource(t, clusterID, cosmosPostgresAPI, func(r map[string]any) error {
		if p := properties(r); fmt.Sprint(p["nodeCount"]) != "0" || p["postgresqlVersion"] != "16" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	onCluster := owner("citus")
	rule := &citus.ServerGroupsv2FirewallRule{}
	rule.Namespace, rule.Name = ns, "office"
	rule.Spec = citus.ServerGroupsv2FirewallRule_Spec{
		AzureName: "office", Owner: onCluster, StartIpAddress: ptr("203.0.113.10"), EndIpAddress: ptr("203.0.113.10"),
	}
	role := &citus.ServerGroupsv2Role{}
	role.Namespace, role.Name = ns, "app"
	role.Spec = citus.ServerGroupsv2Role_Spec{
		AzureName: "app", Owner: onCluster, Password: &genruntime.SecretReference{Name: "citus", Key: "app"},
	}
	config := &citus.ServerGroupsv2CoordinatorConfiguration{}
	config.Namespace, config.Name = ns, "work-mem"
	config.Annotations = map[string]string{"serviceoperator.azure.com/reconcile-policy": "detach-on-delete"}
	config.Spec = citus.ServerGroupsv2CoordinatorConfiguration_Spec{AzureName: "work_mem", Owner: onCluster, Value: ptr("16384")}
	create(t, 20*time.Minute, rule, role, config)
	expectResource(t, clusterID+"/roles/app", cosmosPostgresAPI, func(r map[string]any) error { return nil })
	expectResource(t, clusterID+"/coordinatorConfigurations/work_mem", cosmosPostgresAPI, func(r map[string]any) error {
		if v := properties(r)["value"]; !strings.EqualFold(fmt.Sprint(v), "16384") {
			return fmt.Errorf("value %v", v)
		}
		return nil
	})

	update(t, rule, func(r *citus.ServerGroupsv2FirewallRule) { r.Spec.EndIpAddress = ptr("203.0.113.20") })
	eventually(t, 20*time.Minute, "the firewall rule Ready after the change", ready(rule))
	expectResource(t, clusterID+"/firewallRules/office", cosmosPostgresAPI, func(r map[string]any) error {
		if properties(r)["endIpAddress"] != "203.0.113.20" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	remove(t, 20*time.Minute, config)
	remove(t, 20*time.Minute, role)
	expectGone(t, clusterID+"/roles/app", cosmosPostgresAPI)
	remove(t, 20*time.Minute, rule)
	expectGone(t, clusterID+"/firewallRules/office", cosmosPostgresAPI)
	remove(t, 45*time.Minute, cluster)
	expectGone(t, clusterID, cosmosPostgresAPI)
}
