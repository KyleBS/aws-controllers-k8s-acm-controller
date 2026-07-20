# Copyright Amazon.com Inc. or its affiliates. All Rights Reserved.
#
# Licensed under the Apache License, Version 2.0 (the "License"). You may
# not use this file except in compliance with the License. A copy of the
# License is located at
#
#	 http://aws.amazon.com/apache2.0/
#
# or in the "license" file accompanying this file. This file is distributed
# on an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either
# express or implied. See the License for the specific language governing
# permissions and limitations under the License.

"""Integration tests for the ACM AcmeEndpoint resource
"""

import time
import pytest

from typing import Dict, Tuple
from acktest.k8s import resource as k8s
from acktest.resources import random_suffix_name
from acktest import tags
from e2e import service_marker, CRD_GROUP, CRD_VERSION, load_resource
from e2e.replacement_values import REPLACEMENT_VALUES

ACME_ENDPOINT_PLURAL = 'acmeendpoints'

# AcmeEndpoint goes CREATING -> ACTIVE, requeue is 30s
CREATE_ENDPOINT_WAIT_SECONDS = 35
# Time to allow an update (tag sync / field patch) to reconcile
UPDATE_WAIT_SECONDS = 35


def _aws_resource_tags(acm_client, resource_arn: str) -> Dict[str, str]:
    """Returns the AWS-side tags for a resource ARN as a {key: value} dict,
    using the standardized ListTagsForResource API (the source of truth)."""
    resp = acm_client.list_tags_for_resource(ResourceArn=resource_arn)
    return {t["Key"]: t.get("Value") for t in resp.get("Tags", [])}


@pytest.fixture
def acme_endpoint(request) -> Tuple[k8s.CustomResourceReference, Dict]:
    endpoint_name = random_suffix_name("acme-endpoint", 20)

    replacements = REPLACEMENT_VALUES.copy()
    replacements['ACME_ENDPOINT_NAME'] = endpoint_name

    resource_data = load_resource(
        "acme_endpoint",
        additional_replacements=replacements,
    )

    ref = k8s.CustomResourceReference(
        CRD_GROUP, CRD_VERSION, ACME_ENDPOINT_PLURAL,
        endpoint_name, namespace="default",
    )
    k8s.create_custom_resource(ref, resource_data)
    cr = k8s.wait_resource_consumed_by_controller(ref)

    assert cr is not None
    assert k8s.get_resource_exists(ref)

    time.sleep(CREATE_ENDPOINT_WAIT_SECONDS)

    yield (ref, cr)

    try:
        _, deleted = k8s.delete_custom_resource(ref, 3, 10)
        assert deleted
    except:
        pass


@service_marker
class TestAcmeEndpoint:
    def test_create_delete(self, acme_endpoint, acm_client):
        (ref, cr) = acme_endpoint

        # Poll for the resource to become synced (the endpoint is ACTIVE
        # once the synced condition is True) to avoid timing inconsistencies.
        assert k8s.wait_on_condition(ref, "ACK.ResourceSynced", "True", wait_periods=3)

        # Re-read to get updated status
        cr = k8s.get_resource(ref)
        assert cr is not None

        # Verify the endpoint reached ACTIVE status
        assert cr["status"].get("status") == "ACTIVE", \
            f"Expected ACTIVE, got {cr['status'].get('status')}"

        # Verify endpointURL is populated
        endpoint_url = cr["status"].get("endpointURL")
        assert endpoint_url is not None, "endpointURL should be set"
        assert "acm-acme" in endpoint_url, \
            f"endpointURL should contain 'acm-acme', got: {endpoint_url}"

        # Verify ARN is set
        arn = cr["status"]["ackResourceMetadata"]["arn"]
        assert arn is not None
        assert "acme-endpoint" in arn

        # Verify against AWS (the source of truth) that the endpoint the
        # controller reports actually matches what ACM has.
        aws = acm_client.describe_acme_endpoint(AcmeEndpointArn=arn)["AcmeEndpoint"]
        assert aws["Status"] == cr["status"]["status"], \
            f"AWS status {aws['Status']} != CR status {cr['status']['status']}"
        assert aws["EndpointUrl"] == endpoint_url, \
            f"AWS endpointURL {aws['EndpointUrl']} != CR {endpoint_url}"
        assert aws["Contact"] == cr["spec"]["contact"]

        # Verify the create-time tag actually landed on the AWS resource.
        aws_tags = _aws_resource_tags(acm_client, arn)
        assert aws_tags is not None
        tags.assert_equal_without_ack_tags(
            expected={"environment": "dev"}, actual=aws_tags,
        )

    def test_update(self, acme_endpoint, acm_client):
        (ref, cr) = acme_endpoint
        cr = k8s.get_resource(ref)
        arn = cr["status"]["ackResourceMetadata"]["arn"]

        # Update a non-tag mutable field (contact) to exercise the
        # UpdateAcmeEndpoint path, and simultaneously rewrite the tag set
        # (remove "environment", add "team") to exercise tag sync
        # (TagResource + UntagResource).
        updates = {
            "spec": {
                "contact": "REQUIRED",
                "certificateAuthority": {
                    "publicCertificateAuthority": {
                        "allowedKeyAlgorithms": ["RSA_2048", "EC_prime256v1"],
                    },
                },
                "tags": [{"key": "team", "value": "platform"}],
            }
        }
        k8s.patch_custom_resource(ref, updates)
        time.sleep(UPDATE_WAIT_SECONDS)

        # The update should reconcile fully and return to a synced state.
        assert k8s.wait_on_condition(ref, "ACK.ResourceSynced", "True", wait_periods=3)

        # Verify against AWS that both the field update and the tag sync
        # reached ACM.
        aws = acm_client.describe_acme_endpoint(AcmeEndpointArn=arn)["AcmeEndpoint"]
        assert aws["Contact"] == "REQUIRED", \
            f"expected AWS contact REQUIRED, got {aws['Contact']}"

        aws_algorithms = aws.get("CertificateAuthority", {}) \
            .get("PublicCertificateAuthority", {}).get("AllowedKeyAlgorithms")
        assert aws_algorithms == ["RSA_2048", "EC_prime256v1"], \
            f"expected updated allowedKeyAlgorithms on AWS, got {aws_algorithms}"

        aws_tags = _aws_resource_tags(acm_client, arn)
        # The user-managed tag set was rewritten: "team" added, "environment"
        # removed. assert_equal_without_ack_tags ignores ACK's own
        # services.k8s.aws/* tags and asserts the user tag set matches exactly
        # (mirrors the tag assertions in test_certificate.py).
        tags.assert_equal_without_ack_tags(
            expected={"team": "platform"}, actual=aws_tags,
        )
