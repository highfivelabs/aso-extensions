/*
Copyright (c) Microsoft Corporation.
Licensed under the MIT license.
*/

// Modified by High Five Labs: copied from ASO's
// api/network/customizations/route_table_extensions.go (the body of
// RouteTableExtension.ModifyARMResource), made a function of the child
// collections to keep, so an ExpressRoute circuit keeps its peerings and
// authorizations and a peering its Global Reach connections, each of which is
// its own kind, when the parent is PUT again; and the helpers it calls, from
// api/network/customizations/virtual_network_extensions.go
// (getChildCollectionField, getRawChildCollection, setChildCollection,
// fuzzySetResource), unchanged. Copied rather than imported: importing ASO's
// network customizations package would build in ASO's network API packages and
// the Azure SDK's armnetwork module. Changed: the ARM client and kube client
// come through pkg/outoftree; a collection Azure did not return is left unset
// rather than an error; the log level is a number (ASO's internal
// logging.Info is 1). Kept in generator/_extensions and copied into apis/arm
// by hack/generate-arm.sh, which deletes apis/arm on every run.

package customizations

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/go-logr/logr"
	"github.com/rotisserie/eris"

	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"
	"github.com/Azure/azure-service-operator/v2/pkg/outoftree"
)

// childCollection is a collection of child resources a parent's ARM payload
// carries: its JSON name under "properties" and its Go field under
// Properties of the ARM type.
type childCollection struct {
	jsonName  string
	fieldName string
}

// keepChildCollections sets each collection of armObj to what Azure holds,
// so a PUT of the parent does not remove children that are their own kinds.
func keepChildCollections(
	ctx context.Context,
	armClient *outoftree.GenericClient,
	armObj genruntime.ARMResource,
	obj genruntime.ARMMetaObject,
	kubeClient outoftree.KubeClient,
	log logr.Logger,
	collections ...childCollection,
) (genruntime.ARMResource, error) {
	resourceID, hasResourceID := genruntime.GetResourceID(obj)
	if !hasResourceID {
		// If we don't have an ARM ID yet, we've not been claimed so just return armObj as is
		return armObj, nil
	}

	apiVersion, err := genruntime.GetAPIVersion(obj, kubeClient.Scheme())
	if err != nil {
		return nil, eris.Wrapf(err, "error getting api version for resource %s while getting status", obj.GetName())
	}

	// Get the raw resource
	raw := make(map[string]any)
	_, err = armClient.GetByID(ctx, resourceID, apiVersion, &raw)
	if err != nil {
		// If the error is NotFound, the resource we're trying to Create doesn't exist and so no modification is needed
		var responseError *azcore.ResponseError
		if eris.As(err, &responseError) && responseError.StatusCode == http.StatusNotFound {
			return armObj, nil
		}
		return nil, eris.Wrapf(err, "getting resource with ID: %q", resourceID)
	}

	props, _ := raw["properties"].(map[string]any)
	for _, c := range collections {
		if _, ok := props[c.jsonName]; !ok {
			continue
		}

		children, err := getRawChildCollection(raw, c.jsonName)
		if err != nil {
			return nil, eris.Wrapf(err, "failed to get %s", c.jsonName)
		}

		log.V(1).Info("Found children to include on "+obj.GetObjectKind().GroupVersionKind().Kind, "collection", c.jsonName, "count", len(children), "names", genruntime.RawNames(children))

		err = setChildCollection(armObj.Spec(), children, c.fieldName)
		if err != nil {
			return nil, eris.Wrapf(err, "failed to set %s", c.jsonName)
		}
	}

	return armObj, nil
}

func getChildCollectionField(parent any, childFieldName string) (ret reflect.Value, err error) {
	defer func() {
		if x := recover(); x != nil {
			err = eris.Errorf("caught panic: %s", x)
		}
	}()

	// Here be dragons
	parentValue := reflect.ValueOf(parent)
	parentValue = reflect.Indirect(parentValue)
	if !parentValue.IsValid() {
		return reflect.Value{}, eris.Errorf("cannot assign to nil parent")
	}

	propertiesField := parentValue.FieldByName("Properties")
	if !propertiesField.IsValid() {
		return reflect.Value{}, eris.Errorf("couldn't find properties field")
	}

	propertiesValue := reflect.Indirect(propertiesField)
	if !propertiesValue.IsValid() {
		// If the properties field is nil, we must construct an entirely new properties and assign it here
		temp := reflect.New(propertiesField.Type().Elem())
		propertiesField.Set(temp)
		propertiesValue = reflect.Indirect(temp)
	}

	childField := propertiesValue.FieldByName(childFieldName)
	if !childField.IsValid() {
		return reflect.Value{}, eris.Errorf("couldn't find %q field", childFieldName)
	}

	if childField.Type().Kind() != reflect.Slice {
		return reflect.Value{}, eris.Errorf("%q field was not of kind Slice", childFieldName)
	}

	return childField, nil
}

func getRawChildCollection(parent map[string]any, childJSONName string) ([]any, error) {
	props, ok := parent["properties"]
	if !ok {
		return nil, eris.Errorf("couldn't find properties field")
	}

	propsMap, ok := props.(map[string]any)
	if !ok {
		return nil, eris.Errorf("properties field wasn't a map")
	}

	childField, ok := propsMap[childJSONName]
	if !ok {
		return nil, eris.Errorf("couldn't find %q field", childJSONName)
	}

	childSlice, ok := childField.([]any)
	if !ok {
		return nil, eris.Errorf("%q field wasn't a slice", childJSONName)
	}

	return childSlice, nil
}

func setChildCollection(parent genruntime.ARMResourceSpec, childCollectionFromAzure []any, childFieldName string) (err error) {
	defer func() {
		if x := recover(); x != nil {
			err = eris.Errorf("caught panic: %s", x)
		}
	}()

	childField, err := getChildCollectionField(parent, childFieldName)
	if err != nil {
		return err
	}

	elemType := childField.Type().Elem()
	childSlice := reflect.MakeSlice(childField.Type(), 0, 0)

	for _, child := range childCollectionFromAzure {
		embeddedResource := reflect.New(elemType)
		err = fuzzySetResource(child, embeddedResource)
		if err != nil {
			return err
		}

		childSlice = reflect.Append(childSlice, reflect.Indirect(embeddedResource))
	}

	childField.Set(childSlice)

	return nil
}

func fuzzySetResource(resource any, embeddedResource reflect.Value) error {
	resourceJSON, err := json.Marshal(resource)
	if err != nil {
		return eris.Wrap(err, "failed to marshal resource JSON")
	}

	err = json.Unmarshal(resourceJSON, embeddedResource.Interface())
	if err != nil {
		return eris.Wrap(err, "failed to unmarshal resource JSON")
	}

	// TODO: Can't do a trivial fuzzyEqualityComparison here because we don't know which fields are readonly
	// TODO: and which are not. This results in mismatches like dropping etag and other fields.

	return nil
}
