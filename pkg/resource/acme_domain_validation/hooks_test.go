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
	"strings"
	"testing"

	svcapitypes "github.com/aws-controllers-k8s/acm-controller/apis/v1alpha1"
)

func strPtr(s string) *string { return &s }

func TestDomainValidationModifiable(t *testing.T) {
	cases := []struct {
		name   string
		status *string
		want   bool
	}{
		{"nil status", nil, false},
		{"validating", strPtr("VALIDATING"), false},
		{"valid", strPtr("VALID"), true},
		{"invalid", strPtr("INVALID"), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := &resource{ko: &svcapitypes.AcmeDomainValidation{}}
			r.ko.Status.Status = tc.status
			if got := domainValidationModifiable(r); got != tc.want {
				t.Errorf("domainValidationModifiable(status=%v) = %v, want %v", tc.status, got, tc.want)
			}
		})
	}
}

func TestRequeueWaitUntilCanModify(t *testing.T) {
	r := &resource{ko: &svcapitypes.AcmeDomainValidation{}}

	if got := requeueWaitUntilCanModify(r); got != nil {
		t.Errorf("expected nil requeue for nil status, got %v", got)
	}

	r.ko.Status.Status = strPtr("VALIDATING")
	got := requeueWaitUntilCanModify(r)
	if got == nil {
		t.Fatal("expected a requeue error for VALIDATING status, got nil")
	}
	if !strings.Contains(got.Error(), "VALIDATING") {
		t.Errorf("requeue message should mention current status, got %q", got.Error())
	}
}
