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

"""Integration tests for the ACM API ACME resources
"""

import time
import os
import pytest

from typing import Dict, Tuple
from kubernetes import client
from acktest.k8s import resource as k8s
from acktest.resources import random_suffix_name
from acktest import tags
from e2e import service_marker, CRD_GROUP, CRD_VERSION, load_resource
from e2e.replacement_values import REPLACEMENT_VALUES

ACME_ENDPOINT_PLURAL = 'acmeendpoints'
ACME_EAB_PLURAL = 'acmeexternalaccountbindings'
ACME_DOMAIN_VALIDATION_PLURAL = 'acmedomainvalidations'

# AcmeEndpoint goes CREATING -> ACTIVE, requeue is 30s
CREATE_ENDPOINT_WAIT_SECONDS = 35
# Domain validation goes VALIDATING -> VALID, can take 60s+
CREATE_DOMAIN_VALIDATION_WAIT_SECONDS = 65
# EAB creation is near-instant, requeue is 60s
CREATE_EAB_WAIT_SECONDS = 65
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
                "tags": [{"key": "team", "value": "platform"}],
            }
        }
        k8s.patch_custom_resource(ref, updates)
        time.sleep(UPDATE_WAIT_SECONDS)

        # Verify against AWS that both the field update and the tag sync
        # reached ACM.
        aws = acm_client.describe_acme_endpoint(AcmeEndpointArn=arn)["AcmeEndpoint"]
        assert aws["Contact"] == "REQUIRED", \
            f"expected AWS contact REQUIRED, got {aws['Contact']}"

        aws_tags = _aws_resource_tags(acm_client, arn)
        # The user-managed tag set was rewritten: "team" added, "environment"
        # removed. assert_equal_without_ack_tags ignores ACK's own
        # services.k8s.aws/* tags and asserts the user tag set matches exactly
        # (mirrors the tag assertions in test_certificate.py).
        tags.assert_equal_without_ack_tags(
            expected={"team": "platform"}, actual=aws_tags,
        )


@pytest.fixture
def acme_endpoint_with_eab(request, acme_endpoint) -> Tuple[k8s.CustomResourceReference, Dict, str]:
    """Creates an AcmeEndpoint and then an EAB for it."""
    (endpoint_ref, endpoint_cr) = acme_endpoint

    # Re-read endpoint to get ARN
    endpoint_cr = k8s.get_resource(endpoint_ref)
    endpoint_arn = endpoint_cr["status"]["ackResourceMetadata"]["arn"]

    eab_name = random_suffix_name("acme-eab", 20)
    secret_name = eab_name + "-credentials"

    # The controller populates an existing Secret with the EAB credentials; it
    # does not create one. Users are expected to create the Secret first (same
    # pattern as the Certificate exportTo field).
    v1 = client.CoreV1Api(k8s._get_k8s_api_client())
    secret_body = client.V1Secret(
        metadata=client.V1ObjectMeta(name=secret_name, namespace="default"),
        type="Opaque",
    )
    v1.create_namespaced_secret("default", secret_body)

    replacements = REPLACEMENT_VALUES.copy()
    replacements['ACME_EAB_NAME'] = eab_name
    replacements['ACME_EAB_SECRET_NAME'] = secret_name
    replacements['ACME_ENDPOINT_ARN'] = endpoint_arn
    replacements['ROLE_ARN'] = os.environ.get(
        'ACME_ROLE_ARN', REPLACEMENT_VALUES.get('ACME_ROLE_ARN', '')
    )

    resource_data = load_resource(
        "acme_external_account_binding",
        additional_replacements=replacements,
    )

    ref = k8s.CustomResourceReference(
        CRD_GROUP, CRD_VERSION, ACME_EAB_PLURAL,
        eab_name, namespace="default",
    )
    k8s.create_custom_resource(ref, resource_data)
    cr = k8s.wait_resource_consumed_by_controller(ref)

    assert cr is not None
    assert k8s.get_resource_exists(ref)

    time.sleep(CREATE_EAB_WAIT_SECONDS)

    yield (ref, cr, endpoint_arn)

    try:
        _, deleted = k8s.delete_custom_resource(ref, 3, 10)
        assert deleted
    except:
        pass

    try:
        v1.delete_namespaced_secret(secret_name, "default")
    except:
        pass


@service_marker
class TestAcmeExternalAccountBinding:
    def test_create_delete_and_credentials_secret(self, acme_endpoint_with_eab, acm_client):
        (ref, cr, endpoint_arn) = acme_endpoint_with_eab

        # Re-read to get updated status
        cr = k8s.get_resource(ref)
        assert cr is not None

        # Verify ARN is set
        arn = cr["status"]["ackResourceMetadata"]["arn"]
        assert arn is not None
        assert "acme-external-account-binding" in arn

        # Verify the key identifier is surfaced in status for ACME clients
        key_id_status = cr["status"].get("keyID")
        assert key_id_status is not None, "status.keyID should be set"
        assert len(key_id_status) > 0, "status.keyID should not be empty"

        # Verify the actual K8s Secret was populated with the credentials.
        # The sensitive macKey is written under the user-specified key, and the
        # keyId is written under a fixed "keyId" key.
        secret_name = cr["spec"]["credentialsOutput"]["name"]
        secret_namespace = cr["spec"]["credentialsOutput"].get("namespace", "default")
        mac_key_field = cr["spec"]["credentialsOutput"]["key"]

        v1 = client.CoreV1Api(k8s._get_k8s_api_client())
        secret = v1.read_namespaced_secret(secret_name, secret_namespace)
        assert secret is not None, f"Secret {secret_name} should exist"
        assert "keyId" in secret.data, "Secret should contain keyId"
        assert mac_key_field in secret.data, f"Secret should contain macKey under '{mac_key_field}'"

        # Verify the credentials are non-empty
        key_id = secret.data["keyId"]
        mac_key = secret.data[mac_key_field]
        assert len(key_id) > 0, "keyId should not be empty"
        assert len(mac_key) > 0, "macKey should not be empty"

        # Verify against AWS (the source of truth) that the EAB exists and that
        # the create-time tag landed.
        aws = acm_client.describe_acme_external_account_binding(
            AcmeExternalAccountBindingArn=arn,
        )["ExternalAccountBinding"]
        assert aws["AcmeExternalAccountBindingArn"] == arn
        aws_tags = _aws_resource_tags(acm_client, arn)
        assert aws_tags is not None
        tags.assert_equal_without_ack_tags(
            expected={"environment": "dev"}, actual=aws_tags,
        )

    def test_update_tags(self, acme_endpoint_with_eab, acm_client):
        (ref, cr, endpoint_arn) = acme_endpoint_with_eab
        cr = k8s.get_resource(ref)
        arn = cr["status"]["ackResourceMetadata"]["arn"]

        # The EAB has no service-side Update operation; its sdkUpdate is a
        # custom method that reconciles only tags via TagResource/UntagResource.
        # Rewrite the tag set (remove "environment", add "team") and verify the
        # change reaches AWS.
        k8s.patch_custom_resource(
            ref, {"spec": {"tags": [{"key": "team", "value": "platform"}]}},
        )
        time.sleep(UPDATE_WAIT_SECONDS)

        aws_tags = _aws_resource_tags(acm_client, arn)
        # The user-managed tag set was rewritten: "team" added, "environment"
        # removed. assert_equal_without_ack_tags ignores ACK's own
        # services.k8s.aws/* tags and asserts the user tag set matches exactly
        # (mirrors the tag assertions in test_certificate.py).
        tags.assert_equal_without_ack_tags(
            expected={"team": "platform"}, actual=aws_tags,
        )


@pytest.fixture
def acme_domain_validation(request, acme_endpoint) -> Tuple[k8s.CustomResourceReference, Dict]:
    """Creates an AcmeEndpoint and then a DomainValidation for it."""
    (endpoint_ref, endpoint_cr) = acme_endpoint

    # Re-read endpoint to get ARN
    endpoint_cr = k8s.get_resource(endpoint_ref)
    endpoint_arn = endpoint_cr["status"]["ackResourceMetadata"]["arn"]

    validation_name = random_suffix_name("acme-dv", 20)

    replacements = REPLACEMENT_VALUES.copy()
    replacements['ACME_DOMAIN_VALIDATION_NAME'] = validation_name
    replacements['ACME_ENDPOINT_ARN'] = endpoint_arn
    replacements['DOMAIN_NAME'] = 'example.com'

    resource_data = load_resource(
        "acme_domain_validation",
        additional_replacements=replacements,
    )

    ref = k8s.CustomResourceReference(
        CRD_GROUP, CRD_VERSION, ACME_DOMAIN_VALIDATION_PLURAL,
        validation_name, namespace="default",
    )
    k8s.create_custom_resource(ref, resource_data)
    cr = k8s.wait_resource_consumed_by_controller(ref)

    assert cr is not None
    assert k8s.get_resource_exists(ref)

    time.sleep(CREATE_DOMAIN_VALIDATION_WAIT_SECONDS)

    yield (ref, cr)

    try:
        _, deleted = k8s.delete_custom_resource(ref, 3, 10)
        assert deleted
    except:
        pass


@service_marker
class TestAcmeDomainValidation:
    def test_create_delete(self, acme_domain_validation, acm_client):
        (ref, cr) = acme_domain_validation

        # Re-read to get updated status
        cr = k8s.get_resource(ref)
        assert cr is not None

        # NOTE: The domain validation request will quickly transition from
        # VALIDATING to INVALID, so this just checks to make sure we're
        # in one of those states...
        status = cr["status"].get("status")
        assert status in ("INVALID", "VALIDATING"), \
            f"Expected INVALID or VALIDATING, got {status}"

        # Verify ARN is set
        arn = cr["status"]["ackResourceMetadata"]["arn"]
        assert arn is not None

        # Verify against AWS (the source of truth) that the domain validation
        # exists, its status agrees, and the create-time tag landed.
        aws = acm_client.describe_acme_domain_validation(
            AcmeDomainValidationArn=arn,
        )["AcmeDomainValidation"]
        assert aws["Status"] == status, \
            f"AWS status {aws['Status']} != CR status {status}"
        aws_tags = _aws_resource_tags(acm_client, arn)
        assert aws_tags is not None
        tags.assert_equal_without_ack_tags(
            expected={"environment": "dev"}, actual=aws_tags,
        )

    def test_update_tags(self, acme_domain_validation, acm_client):
        (ref, cr) = acme_domain_validation
        cr = k8s.get_resource(ref)
        arn = cr["status"]["ackResourceMetadata"]["arn"]

        # Rewrite the tag set (remove "environment", add "team") to exercise
        # tag sync on update, and verify the change reaches AWS.
        k8s.patch_custom_resource(
            ref, {"spec": {"tags": [{"key": "team", "value": "platform"}]}},
        )
        time.sleep(UPDATE_WAIT_SECONDS)

        aws_tags = _aws_resource_tags(acm_client, arn)
        # The user-managed tag set was rewritten: "team" added, "environment"
        # removed. assert_equal_without_ack_tags ignores ACK's own
        # services.k8s.aws/* tags and asserts the user tag set matches exactly
        # (mirrors the tag assertions in test_certificate.py).
        tags.assert_equal_without_ack_tags(
            expected={"team": "platform"}, actual=aws_tags,
        )
