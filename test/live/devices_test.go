// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

//go:build live

package live

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"fmt"
	"math/big"
	"testing"
	"time"

	dps "github.com/highfivelabs/aso-extensions/apis/arm/devices/v20261101"
)

const dpsAPI = "2026-11-01"

// A Device Provisioning Service with a shared access policy and a CA
// certificate made for the run (docs/plan.md f75): created, the allocation
// policy changed, deleted. Cheap: the S1 tier bills operations (none is
// made), not hours. Not here: an IoT hub's certificates and connections
// (a hub of its own: a Free hub is one a subscription) and linking a hub.
func TestLiveProvisioningService(t *testing.T) {
	t.Parallel()
	requireProvider(t, "Microsoft.Devices")
	ns := namespace(t, "dps")
	name := azName("dps")

	service := &dps.ProvisioningService{}
	service.Namespace, service.Name = ns, "enroll"
	service.Spec = dps.ProvisioningService_Spec{AzureName: name, Owner: owner("rg"), Location: ptr(location),
		Sku: &dps.IotDpsSkuInfo{Name: ptr(dps.IotDpsSku("S1")), Capacity: ptr(1)},
		Properties: &dps.IotDpsPropertiesDescription{AllocationPolicy: ptr(dps.AllocationPolicy_Hashed),
			AuthorizationPolicies: []dps.SharedAccessSignatureAuthorizationRuleAccessRightsDescription{{
				KeyName: ptr("provisioningserviceowner"), Rights: ptr(dps.AccessRightsDescription_ServiceConfig)}}},
	}
	create(t, timeout, service)
	cert := &dps.ProvisioningServicesCertificate{}
	cert.Namespace, cert.Name = ns, "root"
	cert.Spec = dps.ProvisioningServicesCertificate_Spec{AzureName: "root", Owner: owner("enroll"),
		Properties: &dps.CertificateProperties{Certificate: ptr(selfSignedCA(t, "aso-extensions live "+name))}}
	create(t, timeout, cert)

	dpsID := rgID() + "/providers/Microsoft.Devices/provisioningServices/" + name
	expectResource(t, dpsID+"/certificates/root", dpsAPI, func(r map[string]any) error {
		if p := properties(r); p["subject"] == nil {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	update(t, service, func(s *dps.ProvisioningService) {
		s.Spec.Properties.AllocationPolicy = ptr(dps.AllocationPolicy_GeoLatency)
	})
	eventually(t, timeout, "the service Ready after the change", ready(service))
	expectResource(t, dpsID, dpsAPI, func(r map[string]any) error {
		if p := properties(r); p["allocationPolicy"] != "GeoLatency" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	remove(t, timeout, cert)
	expectGone(t, dpsID+"/certificates/root", dpsAPI)
	remove(t, timeout, service)
	expectGone(t, dpsID, dpsAPI)
}

// selfSignedCA is a CA certificate made for the run, base64 DER, with no
// private key kept.
func selfSignedCA(t *testing.T, subject string) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: subject},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(der)
}
