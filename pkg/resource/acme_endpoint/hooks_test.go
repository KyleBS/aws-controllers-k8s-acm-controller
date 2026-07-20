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
	"strings"
	"testing"

	svcapitypes "github.com/aws-controllers-k8s/acm-controller/apis/v1alpha1"
)

func strPtr(s string) *string { return &s }

func TestEndpointActive(t *testing.T) {
	cases := []struct {
		name   string
		status *string
		want   bool
	}{
		{"nil status", nil, false},
		{"creating", strPtr("CREATING"), false},
		{"deleting", strPtr("DELETING"), false},
		{"failed", strPtr("FAILED"), false},
		{"active", strPtr("ACTIVE"), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := &resource{ko: &svcapitypes.AcmeEndpoint{}}
			r.ko.Status.Status = tc.status
			if got := endpointActive(r); got != tc.want {
				t.Errorf("endpointActive(status=%v) = %v, want %v", tc.status, got, tc.want)
			}
		})
	}
}

func TestRequeueWaitUntilCanModify(t *testing.T) {
	r := &resource{ko: &svcapitypes.AcmeEndpoint{}}

	if got := requeueWaitUntilCanModify(r); got != nil {
		t.Errorf("expected nil requeue for nil status, got %v", got)
	}

	r.ko.Status.Status = strPtr("CREATING")
	got := requeueWaitUntilCanModify(r)
	if got == nil {
		t.Fatal("expected a requeue error for CREATING status, got nil")
	}
	if !strings.Contains(got.Error(), "CREATING") || !strings.Contains(got.Error(), StatusActive) {
		t.Errorf("requeue message should mention current and required status, got %q", got.Error())
	}
}
