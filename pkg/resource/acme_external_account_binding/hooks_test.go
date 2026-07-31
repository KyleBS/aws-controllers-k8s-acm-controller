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
	"strings"
	"testing"

	ackv1alpha1 "github.com/aws-controllers-k8s/runtime/apis/core/v1alpha1"
	ackcompare "github.com/aws-controllers-k8s/runtime/pkg/compare"
	ackerr "github.com/aws-controllers-k8s/runtime/pkg/errors"

	svcapitypes "github.com/aws-controllers-k8s/acm-controller/apis/v1alpha1"
)

func strPtr(s string) *string { return &s }

func eabResource() *resource {
	arn := ackv1alpha1.AWSResourceName(
		"arn:aws:acm:us-west-2:123456789012:acme-endpoint/e1/acme-external-account-binding/b1")
	ko := &svcapitypes.AcmeExternalAccountBinding{}
	ko.Status.ACKResourceMetadata = &ackv1alpha1.ResourceMetadata{ARN: &arn}
	ko.Spec.Tags = []*svcapitypes.Tag{{Key: strPtr("k"), Value: strPtr("v")}}
	return &resource{ko: ko}
}

// The service has no update operation for an external account binding, so the only
// reconcilable field is the tag set. Anything else must be a TERMINAL error: a plain
// error would be retried for ever, and returning nil would leave the resource reporting
// Synced while its spec and the service disagreed.
//
// The tag-sync path itself is exercised by the e2e test (test_update_tags); it cannot be
// stubbed from here because tags.SyncResourceTags takes package-private interfaces.
func TestCustomUpdateRejectsNonTagChanges(t *testing.T) {
	for _, field := range []string{"Spec.RoleARN", "Spec.AcmeEndpointARN", "Spec.Expiration"} {
		t.Run(field, func(t *testing.T) {
			delta := ackcompare.NewDelta()
			delta.Add(field, nil, nil)

			rm := &resourceManager{}
			out, err := rm.customUpdateAcmeExternalAccountBinding(
				context.Background(), eabResource(), eabResource(), delta)

			if err == nil {
				t.Fatalf("changing %s must fail: the service cannot apply it", field)
			}
			if out != nil {
				t.Error("no resource should be returned when the update is rejected")
			}
			var terminal *ackerr.TerminalError
			if !errors.As(err, &terminal) {
				t.Errorf("the error must be a TerminalError so the resource stops retrying, got %T: %v", err, err)
			}
			if !strings.Contains(err.Error(), "only tags") {
				t.Errorf("the message should say what IS updatable, got %q", err.Error())
			}
		})
	}
}

// A delta with no differences at all must be a no-op that returns the desired resource,
// not an error: the runtime calls sdkUpdate whenever it sees any delta, including ones
// confined to fields this resource ignores.
func TestCustomUpdateWithNoDifferencesIsANoOp(t *testing.T) {
	desired := eabResource()
	rm := &resourceManager{}
	out, err := rm.customUpdateAcmeExternalAccountBinding(
		context.Background(), desired, eabResource(), ackcompare.NewDelta())
	if err != nil {
		t.Fatalf("an empty delta must not error, got %v", err)
	}
	if out != desired {
		t.Error("an empty delta should return the desired resource unchanged")
	}
}
