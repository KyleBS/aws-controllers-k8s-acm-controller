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
	"github.com/aws-controllers-k8s/acm-controller/pkg/tags"
)

// syncTags and listTags manage resource tags via the standardized ACM
// TagResource/UntagResource/ListTagsForResource operations. They are wired
// into the generated sdkUpdate and sdkFind flows via hook templates.
var (
	syncTags = tags.SyncResourceTags
	listTags = tags.ListResourceTags
)
