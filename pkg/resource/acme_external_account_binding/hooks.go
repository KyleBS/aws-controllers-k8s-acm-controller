// Copyright Amazon.com Inc. or its affiliates. All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License"). You may
// not use this file except in compliance with the License. A copy of the
// License is located at
//
//     http://aws.amazon.com/apache2.0/
//
// or in the "license" file accompanying this file. This file is distributed
// on an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either
// express or implied. See the License for the specific language governing
// permissions and limitations under the License.

package acme_external_account_binding

import (
	"context"
	"errors"
	"fmt"

	ackcompare "github.com/aws-controllers-k8s/runtime/pkg/compare"
	ackerr "github.com/aws-controllers-k8s/runtime/pkg/errors"
	ackrequeue "github.com/aws-controllers-k8s/runtime/pkg/requeue"

	svcapitypes "github.com/aws-controllers-k8s/acm-controller/apis/v1alpha1"
	svcsdk "github.com/aws/aws-sdk-go-v2/service/acm"

	"github.com/aws-controllers-k8s/acm-controller/pkg/tags"
)

// syncTags and listTags manage resource tags via the standardized ACM
// TagResource/UntagResource/ListTagsForResource operations. They are wired
// into the generated sdkFind flow via a hook template and into sdkUpdate via
// customUpdateAcmeExternalAccountBinding below.
var (
	syncTags = tags.SyncResourceTags
	listTags = tags.ListResourceTags
)

// storeEABCredentials fetches the external account binding's credentials and records
// them: the key identifier into status, and the secret MAC key into the Secret named by
// spec.credentialsOutput.
//
// It is called from BOTH create and read. Create is where it normally happens, but the
// credentials fetch and the Secret write can each fail after the binding already exists
// in ACM (a missing Secret, a denied GetAcmeExternalAccountBindingCredentials, a
// throttle). Doing it only on create would leave such a resource reporting Synced with an
// empty Secret and no keyID, with nothing to retry it — so read repairs it, guarded on
// status.keyID so the steady state costs no extra API call.
//
// Any failure here is returned as a REQUEUE, never as an AWS error: the ACK runtime marks
// a resource unmanaged when Create returns an AWS API error (reconciler.go,
// setResourceUnmanaged), which would drop the finalizer while the binding still exists in
// ACM — deleting the CR would then never delete the binding, leaving a live credential
// nobody tracks.
func (rm *resourceManager) storeEABCredentials(
	ctx context.Context,
	ko *svcapitypes.AcmeExternalAccountBinding,
) error {
	if ko.Status.ACKResourceMetadata == nil || ko.Status.ACKResourceMetadata.ARN == nil {
		// Nothing to fetch credentials for yet.
		return nil
	}
	arn := (*string)(ko.Status.ACKResourceMetadata.ARN)

	resp, err := rm.sdkapi.GetAcmeExternalAccountBindingCredentials(
		ctx, &svcsdk.GetAcmeExternalAccountBindingCredentialsInput{
			AcmeExternalAccountBindingArn: arn,
		})
	rm.metrics.RecordAPICall("READ_ONE", "GetAcmeExternalAccountBindingCredentials", err)
	if err != nil {
		return ackrequeue.NeededAfter(
			// %s, not %w: wrapping would let the runtime see an AWS error and mark the
			// resource unmanaged, dropping the finalizer.
			fmt.Errorf("external account binding created, but its credentials could not be read: %s", err),
			ackrequeue.DefaultRequeueAfterDuration,
		)
	}
	// A binding is only usable with both halves.
	if resp.KeyId == nil || resp.MacKey == nil {
		return ackrequeue.NeededAfter(
			errors.New("GetAcmeExternalAccountBindingCredentials did not return both keyId and macKey"),
			ackrequeue.DefaultRequeueAfterDuration,
		)
	}

	// NOTE: status.keyID is deliberately assigned at the END of this function, after the
	// Secret writes. The ACK runtime patches status even when a reconcile returns an error
	// (reconciler.go, HandleReconcileError -> patchResourceStatus), so assigning it before
	// a write that fails would persist it, and needsCredentials would then see a populated
	// keyID and never retry — leaving the resource Synced with an empty Secret for ever.
	if ko.Spec.CredentialsOutput == nil {
		ko.Status.KeyID = resp.KeyId
		return nil
	}
	if ko.Spec.CredentialsOutput.Name == "" {
		// The schema requires only `key`, so an incomplete reference reaches us here.
		// Terminal rather than a requeue: no amount of retrying fixes a missing name.
		return ackerr.NewTerminalError(
			errors.New("spec.credentialsOutput.name is required in order to write the external account binding credentials"),
		)
	}
	namespace := ko.Spec.CredentialsOutput.Namespace
	if namespace == "" {
		namespace = ko.Namespace
	}
	name := ko.Spec.CredentialsOutput.Name
	macKeyKey := ko.Spec.CredentialsOutput.Key

	// WriteToSecret patches an existing Secret; it does not create one. That is the same
	// contract as Certificate.exportTo.
	if err := rm.rr.WriteToSecret(ctx, *resp.MacKey, namespace, name, macKeyKey); err != nil {
		return ackrequeue.NeededAfter(
			fmt.Errorf("writing the external account binding MAC key to Secret %s/%s: %s", namespace, name, err),
			ackrequeue.DefaultRequeueAfterDuration,
		)
	}
	// The key identifier is also written to the Secret for convenience, under a fixed
	// key — unless the caller chose that key for the MAC key, which must win: the MAC key
	// is the credential, and overwriting it would leave the Secret holding a value that
	// cannot authenticate. status.keyID carries it either way.
	if macKeyKey != "keyId" {
		if err := rm.rr.WriteToSecret(ctx, *resp.KeyId, namespace, name, "keyId"); err != nil {
			return ackrequeue.NeededAfter(
				fmt.Errorf("writing the external account binding key identifier to Secret %s/%s: %s", namespace, name, err),
				ackrequeue.DefaultRequeueAfterDuration,
			)
		}
	}
	// Only now: the credentials are where the user asked for them.
	ko.Status.KeyID = resp.KeyId
	return nil
}

// needsCredentials reports whether the credentials still have to be fetched and stored.
//
// It checks the SECRET, not just status.keyID. Guarding on status alone was not enough: a
// user who adds spec.credentialsOutput to an existing binding, or who deletes or empties
// the Secret, would otherwise never have it (re)populated, because keyID is already set.
// A read of a Secret this controller was pointed at is cheap and involves no AWS call.
func (rm *resourceManager) needsCredentials(
	ctx context.Context,
	ko *svcapitypes.AcmeExternalAccountBinding,
) bool {
	if ko.Spec.CredentialsOutput == nil {
		// Nothing to store; status.keyID is the only output.
		return ko.Status.KeyID == nil
	}
	val, err := rm.rr.SecretValueFromReference(ctx, ko.Spec.CredentialsOutput)
	if err != nil || val == "" {
		// Missing Secret, missing key, or empty value: (re)store. A genuinely broken
		// reference surfaces from storeEABCredentials with a legible error.
		return true
	}
	return ko.Status.KeyID == nil
}

// customUpdateAcmeExternalAccountBinding backs the resource's sdkUpdate. The
// external account binding has no service-side update operation, so the only
// field that can be reconciled after creation is the resource's tag set, which
// is managed through the standardized TagResource/UntagResource API. Any change
// to another field is rejected as a terminal error.
func (rm *resourceManager) customUpdateAcmeExternalAccountBinding(
	ctx context.Context,
	desired *resource,
	latest *resource,
	delta *ackcompare.Delta,
) (*resource, error) {
	if delta.DifferentAt("Spec.Tags") {
		if desired.ko.Status.ACKResourceMetadata == nil || desired.ko.Status.ACKResourceMetadata.ARN == nil {
			return nil, ackerr.NotFound
		}
		if err := syncTags(
			ctx, rm.sdkapi, rm.metrics,
			string(*desired.ko.Status.ACKResourceMetadata.ARN),
			desired.ko.Spec.Tags, latest.ko.Spec.Tags,
		); err != nil {
			return nil, err
		}
	}
	if !delta.DifferentExcept("Spec.Tags") {
		return desired, nil
	}
	return nil, ackerr.NewTerminalError(
		errors.New("only tags can be updated for an external account binding"),
	)
}
