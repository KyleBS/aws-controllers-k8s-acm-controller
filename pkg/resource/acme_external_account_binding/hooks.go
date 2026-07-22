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

	ackcompare "github.com/aws-controllers-k8s/runtime/pkg/compare"
	ackerr "github.com/aws-controllers-k8s/runtime/pkg/errors"

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
