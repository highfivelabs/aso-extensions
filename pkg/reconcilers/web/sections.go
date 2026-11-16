// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

package web

import (
	"context"
	"maps"

	"github.com/go-logr/logr"

	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"

	webv1 "github.com/highfivelabs/aso-extensions/apis/web/v20260715"
	"github.com/highfivelabs/aso-extensions/pkg/webconfig"
)

// appSettings is a SitesConfigAppSettings as a section.
type appSettings struct{ *webv1.SitesConfigAppSettings }

func (s appSettings) object() genruntime.ARMOwnedMetaObject { return s.SitesConfigAppSettings }

func (s appSettings) setID(id string) { s.Status.Id = id }

func (s appSettings) apply(ctx context.Context, log logr.Logger, c *webconfig.Client, id string, v values) error {
	desired := make(map[string]string, len(s.Spec.Settings))
	for _, setting := range s.Spec.Settings {
		value, err := v.value(setting.Name, setting.Value, setting.ValueFromSecret, setting.ValueFromConfig, setting.Template)
		if err != nil {
			return err
		}
		desired[setting.Name] = value
	}
	current, err := c.ListAppSettings(ctx, id)
	if err != nil {
		return err
	}
	if maps.Equal(current, desired) {
		return nil
	}
	log.V(0).Info("Writing the app settings", "names", sortedKeys(desired))
	return c.PutAppSettings(ctx, id, desired)
}

func (s appSettings) read(ctx context.Context, c *webconfig.Client, id string) error {
	current, err := c.ListAppSettings(ctx, id)
	if err != nil {
		return err
	}
	s.Status.Names = sortedKeys(current)
	return nil
}

// connectionStrings is a SitesConfigConnectionStrings as a section.
type connectionStrings struct {
	*webv1.SitesConfigConnectionStrings
}

func (s connectionStrings) object() genruntime.ARMOwnedMetaObject {
	return s.SitesConfigConnectionStrings
}

func (s connectionStrings) setID(id string) { s.Status.Id = id }

func (s connectionStrings) apply(ctx context.Context, log logr.Logger, c *webconfig.Client, id string, v values) error {
	desired := make(map[string]webconfig.ConnectionString, len(s.Spec.ConnectionStrings))
	for _, cs := range s.Spec.ConnectionStrings {
		value, err := v.value(cs.Name, cs.Value, cs.ValueFromSecret, cs.ValueFromConfig, cs.Template)
		if err != nil {
			return err
		}
		desired[cs.Name] = webconfig.ConnectionString{Value: value, Type: cs.Type}
	}
	current, err := c.ListConnectionStrings(ctx, id)
	if err != nil {
		return err
	}
	if maps.Equal(current, desired) {
		return nil
	}
	log.V(0).Info("Writing the connection strings", "names", sortedKeys(desired))
	return c.PutConnectionStrings(ctx, id, desired)
}

func (s connectionStrings) read(ctx context.Context, c *webconfig.Client, id string) error {
	current, err := c.ListConnectionStrings(ctx, id)
	if err != nil {
		return err
	}
	s.Status.ConnectionStrings = nil
	for _, name := range sortedKeys(current) {
		s.Status.ConnectionStrings = append(s.Status.ConnectionStrings, webv1.ConnectionStringStatus{Name: name, Type: current[name].Type})
	}
	return nil
}
