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

package acme_domain_validation

import (
	"fmt"

	ackrequeue "github.com/aws-controllers-k8s/runtime/pkg/requeue"

	"github.com/aws-controllers-k8s/acm-controller/pkg/tags"
)

// StatusValidating is the AcmeDomainValidation status while validation is in
// progress; the service rejects UpdateAcmeDomainValidation in this state.
const StatusValidating = "VALIDATING"

// syncTags and listTags manage resource tags via the standardized ACM
// TagResource/UntagResource/ListTagsForResource operations. They are wired
// into the generated sdkUpdate and sdkFind flows via hook templates.
var (
	syncTags = tags.SyncResourceTags
	listTags = tags.ListResourceTags
)

// domainValidationModifiable returns true if the domain validation can be
// updated. The service rejects updates while validation is in progress
// (VALIDATING); settled states (VALID, INVALID) accept updates.
func domainValidationModifiable(r *resource) bool {
	if r.ko.Status.Status == nil {
		return false
	}
	return *r.ko.Status.Status != StatusValidating
}

// requeueWaitUntilCanModify returns a requeue error for a domain validation
// that is still validating and cannot yet be updated.
func requeueWaitUntilCanModify(r *resource) *ackrequeue.RequeueNeededAfter {
	if r.ko.Status.Status == nil {
		return nil
	}
	status := *r.ko.Status.Status
	return ackrequeue.NeededAfter(
		fmt.Errorf("domain validation in '%s' state, cannot be modified until validation settles",
			status),
		ackrequeue.DefaultRequeueAfterDuration,
	)
}
