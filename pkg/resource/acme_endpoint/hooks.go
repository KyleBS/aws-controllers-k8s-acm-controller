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

package acme_endpoint

import (
	"fmt"

	ackrequeue "github.com/aws-controllers-k8s/runtime/pkg/requeue"

	"github.com/aws-controllers-k8s/acm-controller/pkg/tags"
)

// StatusActive is the AcmeEndpoint status in which the endpoint can serve
// ACME traffic and accept updates.
const StatusActive = "ACTIVE"

// syncTags and listTags manage resource tags via the standardized ACM
// TagResource/UntagResource/ListTagsForResource operations. They are wired
// into the generated sdkUpdate and sdkFind flows via hook templates.
var (
	syncTags = tags.SyncResourceTags
	listTags = tags.ListResourceTags
)

// endpointActive returns true if the endpoint's status is ACTIVE.
func endpointActive(r *resource) bool {
	if r.ko.Status.Status == nil {
		return false
	}
	return *r.ko.Status.Status == StatusActive
}

// requeueWaitUntilCanModify returns a requeue error for an endpoint that is
// not yet in a modifiable (ACTIVE) status.
func requeueWaitUntilCanModify(r *resource) *ackrequeue.RequeueNeededAfter {
	if r.ko.Status.Status == nil {
		return nil
	}
	status := *r.ko.Status.Status
	return ackrequeue.NeededAfter(
		fmt.Errorf("endpoint in '%s' state, cannot be modified until '%s'",
			status, StatusActive),
		ackrequeue.DefaultRequeueAfterDuration,
	)
}
