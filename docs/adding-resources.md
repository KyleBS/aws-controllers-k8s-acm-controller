# Adding a resource to the ACM controller: a complete walkthrough

This guide takes you from nothing to a mergeable PR for a new ACM resource.
It was distilled from adding the ACME resources (`AcmeEndpoint`,
`AcmeDomainValidation`, `AcmeExternalAccountBinding`) — every rule
corresponds to a real review comment, CI failure, or bug from that work, and
every example is real code from this repository. `AcmeEndpoint` is the
running example; `Certificate` and the other ACME resources are your
reference implementations for anything not shown here.

**What a finished single-resource PR looks like** (the actual `AcmeEndpoint`
PR, 36 files). Hand-written:

```
generator.yaml                                          # resource config (Step 3)
documentation.yaml                                      # field docs (Step 5)
README.md                                               # usage section (Step 5)
config/iam/recommended-inline-policy                    # new API actions (Step 5)
templates/hooks/acme_endpoint/delta_pre_compare.go.tpl  # hooks (Step 4)
templates/hooks/acme_endpoint/sdk_update_pre_build_request.go.tpl
templates/hooks/acme_endpoint/sdk_read_one_post_set_output.go.tpl
pkg/resource/acme_endpoint/hooks.go                     # hook helpers (Step 4)
pkg/resource/acme_endpoint/hooks_test.go                # unit tests (Step 4)
test/e2e/resources/acme_endpoint.yaml                   # e2e manifest (Step 7)
test/e2e/tests/test_acme.py                             # e2e tests (Step 7)
```

Everything else (~25 files under `apis/`, `pkg/resource/`, `config/`,
`helm/`) is generated in Step 6 and committed as-is. Note the directory
naming: resource `AcmeEndpoint` → snake_case `acme_endpoint` for both
`templates/hooks/` and `pkg/resource/`.

---

## Step 0 — Environment setup (do this first; nothing else works without it)

Code generation runs from a **separate checkout of the code-generator
repo** — this repo's Makefile only has a `test` target.

```bash
# Sibling checkouts (the layout the scripts expect):
git clone https://github.com/aws-controllers-k8s/code-generator.git
git clone https://github.com/aws-controllers-k8s/runtime.git
git clone <your fork of acm-controller>

cd code-generator
make build-controller SERVICE=acm
```

`make build-controller` looks for the service controller at
`../acm-controller` and the runtime at `../runtime` by default. If your
layout differs, the underlying scripts take environment variables:

```bash
export SERVICE_CONTROLLER_SOURCE_PATH=/path/to/acm-controller
export RUNTIME_CRD_DIR=/path/to/runtime/config     # or the module cache copy
./scripts/build-controller.sh acm                  # codegen + config/ + helm/
```

The generator config (`generator.yaml`), documentation config, and templates
are all read from the service controller checkout; output is written back
into it. Use code-generator `main` unless maintainers say otherwise — an
out-of-date code-generator or runtime produces metadata CI will reject.

**Running the controller locally** (needed for Step 8, useful throughout):
the controller is a single binary that talks to any kubeconfig-reachable
cluster and real AWS.

```bash
# in acm-controller (compiles only after Step 6 has generated your
# resource's pkg/resource/ code — for an existing checkout it works as-is):
go build -o /tmp/acm-controller ./cmd/controller/

# any cluster works: kind, or envtest (just etcd+apiserver, no nodes).
# Real AWS credentials must be resolvable (env vars or ~/.aws) — the
# controller calls STS GetCallerIdentity at startup and exits on failure.
AWS_REGION=us-east-1 KUBECONFIG=$HOME/.kube/config \
  /tmp/acm-controller --aws-region us-east-1 --log-level=debug
# then: kubectl apply -f config/crd/bases/ ; kubectl apply your CRs
```

## Step 1 — Scope the PR

Maintainers release **one resource at a time**; a PR adding several will be
asked to split ("we generally do one release per resource"). If your
resource needs an SDK version bump, land the bump as its own mechanical PR
first, with the new resources parked in `ignore.resource_names`:

```yaml
ignore:
  resource_names:
    - AcmeEndpoint     # added by the SDK-bump PR, removed by the resource PR
```

Your resource PR then removes the name from that list and adds the wiring —
reviewers see only your resource, not SDK churn. Multiple resources: stack
branches (resource 2 on resource 1's branch) and rebase as each merges.

## Step 2 — Audit the API before writing any config

Three questions decide most of the configuration. Answer them with real API
calls, not assumptions.

**2a. Which fields are updatable?** Compare the Create and Update input
structs in the SDK:

```bash
SDKDIR=$(go list -m -f '{{.Dir}}' github.com/aws/aws-sdk-go-v2/service/acm)
awk '/type CreateAcmeEndpointInput struct {/,/noSmithyDocumentSerde/' $SDKDIR/api_op_CreateAcmeEndpoint.go
awk '/type UpdateAcmeEndpointInput struct {/,/noSmithyDocumentSerde/' $SDKDIR/api_op_UpdateAcmeEndpoint.go
```

For `AcmeEndpoint` this shows `CertificateTags` in Create but **not** Update
→ it must be `is_immutable`. A field present in the Update input should stay
mutable (a reviewer will catch either mistake — both happened).

**2b. What does Describe actually return?** For every optional spec field,
create a resource with the field unset and one with it set, and inspect the
raw Describe response (you need an AWS account where the APIs are live —
same account you'll use for e2e):

```python
import boto3
c = boto3.client("acm", region_name="us-east-1")
arn = c.create_acme_endpoint(
    AuthorizationBehavior="PRE_APPROVED",
    CertificateAuthority={"PublicCertificateAuthority": {}},  # field-under-test unset
)["AcmeEndpointArn"]
# ...wait for ACTIVE...
print(c.describe_acme_endpoint(AcmeEndpointArn=arn)["AcmeEndpoint"])
```

You will find one of four behaviors, each with a required handling (Step 4):

| Describe behavior | Example found in ACM | Handling |
|---|---|---|
| Always echoed, server-defaulted when unset | `contact` (defaults to `REQUIRED`) | `late_initialize` |
| **Omitted entirely** when server-defaulted | `certificateAuthority` (empty `{}` = "no restrictions" = nothing stored = nothing returned) | `delta_pre_compare` normalization |
| Returned as a **different read-side shape** | `prevalidationOptions` → reported as `PrevalidationDetails` | read hook reconstructs the spec field |
| Never returned at all | tags (no ACM Describe returns tags) | fetched separately in a read hook |

Skipping this produces **spurious deltas**: a permanent desired-vs-latest
diff firing a no-op `Update*` call every reconcile, forever. e2e will not
catch it (fixtures set every field); Step 8's sweep will.

**2c. Which states allow which operations?** Test each Update (and Delete)
live in each lifecycle state. Real findings: `UpdateAcmeEndpoint` is only
valid on an `ACTIVE` endpoint; `UpdateAcmeDomainValidation` is **rejected**
during `VALIDATING` — and a successful update resets validation. These
become guards in Step 4c.

## Step 3 — Write the `generator.yaml` resource block

The complete, real `AcmeEndpoint` configuration, annotated:

```yaml
operations:                     # map each API operation to the resource
  CreateAcmeEndpoint:
    resource_name: AcmeEndpoint
    operation_type: CREATE
  DescribeAcmeEndpoint:
    resource_name: AcmeEndpoint
    operation_type: READ_ONE
  UpdateAcmeEndpoint:
    resource_name: AcmeEndpoint
    operation_type: UPDATE
  DeleteAcmeEndpoint:
    resource_name: AcmeEndpoint
    operation_type: DELETE

ignore:
  field_paths:
    # The SDK auto-fills idempotency tokens; without this the token becomes
    # a user-visible field in your CRD spec.
    - "CreateAcmeEndpointInput.IdempotencyToken"

resources:
  AcmeEndpoint:
    hooks:                      # Step 4 explains each
      delta_pre_compare:
        template_path: hooks/acme_endpoint/delta_pre_compare.go.tpl
      sdk_update_pre_build_request:
        template_path: hooks/acme_endpoint/sdk_update_pre_build_request.go.tpl
      sdk_read_one_post_set_output:
        template_path: hooks/acme_endpoint/sdk_read_one_post_set_output.go.tpl
    exceptions:
      errors:
        404:
          # Which error code means "gone". Drives both read (resource
          # missing -> create) and delete (already deleted -> success).
          code: ResourceNotFoundException
      terminal_codes:
        # Terminal = reconciliation STOPS until the user edits the spec.
        # Anything transient must stay off this list or the resource wedges
        # permanently: LimitExceededException is retryable quota pressure —
        # NOT terminal (a reviewer removed it from an earlier draft). Beware
        # codes the service uses for both permanent and transient conditions
        # (ValidationException here) — leave those recoverable. Errors NOT
        # in this list are retried by the runtime with backoff.
        - InvalidParameterException
        - InvalidArnException
        - InvalidTagException
        - TagPolicyException
        - TooManyTagsException
    reconcile:
      # How often a settled (synced) resource is re-checked for drift.
      # Rule of thumb: comparable to the resource's own state-transition
      # time — 30s here (endpoint settles in seconds), 60s for Certificate.
      requeue_on_success_seconds: 30
    synced:
      # The endpoint is asynchronous (CREATING -> ACTIVE). ResourceSynced
      # must only be True in settled states. For AcmeDomainValidation this
      # is [VALID, INVALID] — INVALID is settled (user must fix DNS), not
      # transient. Yes, that means Synced=True on a *failed* resource:
      # Synced means "controller state matches AWS and nothing is pending",
      # not "healthy". If your resource has no lifecycle states, omit
      # `synced:` entirely (creation success = synced).
      when:
        - path: Status.Status
          in:
            - ACTIVE
    fields:
      # Server-defaulted AND always echoed by Describe -> late_initialize.
      # Without this, an unset contact diffs nil-vs-REQUIRED forever.
      Contact:
        late_initialize: {}
      # In CreateAcmeEndpointInput but NOT UpdateAcmeEndpointInput (2a).
      CertificateTags:
        is_immutable: true
      # The Create response only returns the ARN. Every other status field
      # must be mapped from the ReadOne response or your status stays empty.
      EndpointUrl:
        is_read_only: true
        from:
          operation: DescribeAcmeEndpoint
          path: AcmeEndpoint.EndpointUrl
      Status:
        is_read_only: true
        from:
          operation: DescribeAcmeEndpoint
          path: AcmeEndpoint.Status
      # ...same pattern for FailureReason, CreatedAt, UpdatedAt...
```

Two constructs most dependent resources also need:

```yaml
      # Cross-resource reference (from AcmeDomainValidation). The generator
      # emits an ADDITIONAL `acmeEndpointRef` spec field: users set either
      # the raw ARN or the ref (name of an AcmeEndpoint CR), and the runtime
      # resolves the ref to Status.ACKResourceMetadata.ARN before reconcile.
      AcmeEndpointArn:
        is_immutable: true
        references:
          resource: AcmeEndpoint
          path: Status.ACKResourceMetadata.ARN

      # Secret-typed field (from AcmeExternalAccountBinding, modeled on
      # Certificate.exportTo): the user names a PRE-EXISTING Secret; the
      # controller writes into it (Step 4d).
      CredentialsOutput:
        type: "bytes"
        is_secret: true
        is_immutable: true
        compare:
          is_ignored: true
```

**Field-config rules (each was a real bug or review comment):**
- `late_initialize` is **only safe for fields the service returns for every
  resource**. A late-initialized field that stays nil requeues every 5
  seconds forever and the resource never reaches `ResourceSynced=True`
  (this took down every Certificate e2e test when `managedBy` — nil for all
  non-managed certificates — was late-initialized).
- `compare.is_ignored` **silently drops user updates** — only for fields
  that genuinely cannot be updated.
- A resource with **no Update operation** (like the EAB): mark every spec
  field `is_immutable` and set
  `update_operation: {custom_method_name: customUpdateAcmeExternalAccountBinding}`;
  Step 4a shows the method.
- **CRD decisions are one-way doors.** Once released, removing or renaming a
  spec/status field is a breaking CRD change. Don't add speculative fields,
  and get immutability right the first time — relaxing `is_immutable` later
  is possible, tightening it against existing resources is not.

**Deletion and adoption come mostly for free — keep them working:** the ACK
runtime manages finalizers and calls the generated `sdkDelete`; the 404
`code:` above makes deletion idempotent (already-gone = success). If the
service rejects Delete in some states, guard it like updates (Step 4c). ACK
also supports adopting existing AWS resources, which exercises your ReadOne
path against resources the controller didn't create — hooks must not assume
any controller-written state exists (derive everything from the ARN and the
Describe response, as the examples here do).

## Step 4 — Hooks: where custom code goes

**How hooks work:** a hook template (`templates/hooks/<snake_case>/*.go.tpl`)
is a fragment of Go **injected verbatim** into a generated function in
`pkg/resource/<snake_case>/sdk.go`. Templates have no import statements —
they can only use packages the generated file already imports
(`ackcondition`, `ackrequeue`, `ackerr`, `corev1`, `svcsdk`,
`svcsdktypes` (SDK enums), `svcapitypes`, `fmt`, `time`, ... — read the
generated `sdk.go` for the full list) plus anything you define in
`hooks.go`. Inside hooks, `rm` is the resource manager: `rm.sdkapi` is the
AWS client, `rm.metrics` the API-call recorder, and `rm.rr` the runtime
reconciler (Kubernetes-side operations such as `WriteToSecret`). `hooks.go` is a
normal hand-written file in the resource package — regeneration never
touches it; put real logic and helpers there and keep templates thin.

Variables available in the common hook points:

| Hook point | Injected into | In scope |
|---|---|---|
| `delta_pre_compare` | `newResourceDelta(a, b)` | `a` = desired, `b` = latest (`*resource`), `delta` |
| `sdk_read_one_post_set_output` | `sdkFind` | `r` = input resource, `ko` = result object being built, `rm`, `ctx`, `resp` |
| `sdk_create_post_set_output` | `sdkCreate` | `desired`/`r`, `ko` (response already mapped — ARN available), `rm`, `ctx`, `resp` |
| `sdk_update_pre_build_request` | `sdkUpdate` | `desired`, `latest`, `delta`, `rm`, `ctx` |

`pre_set_output` vs `post_set_output`: `pre` runs **before** the generated
code copies the API response onto `ko` — anything you write to a
response-mapped field gets overwritten; `post` runs after. Rule of thumb:
read-augmentation and anything needing the ARN go in `post` (a review
comment moved the tag hook from `pre` to `post`).

**4a. Tag support (required for every taggable resource — its absence
bounced the first ACME review).** ACM `Describe*` calls never return tags;
changes go through `TagResource`/`UntagResource`. Three pieces:

`pkg/resource/<resource>/hooks.go` — bind the shared helpers from this
repo's `pkg/tags` package:

```go
import "github.com/aws-controllers-k8s/acm-controller/pkg/tags"

var (
	syncTags = tags.SyncResourceTags   // TagResource/UntagResource diff-sync
	listTags = tags.ListResourceTags   // ListTagsForResource
)
```

(That package also exports `SyncTags`/`ListTags` — those are
Certificate-specific, built on the legacy `*TagsToCertificate` APIs. New
resources use the `*ResourceTags` pair above.)

`templates/hooks/<resource>/sdk_update_pre_build_request.go.tpl` — sync on
update; skip the service Update call when only tags changed:

```go
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
```

`templates/hooks/<resource>/sdk_read_one_post_set_output.go.tpl` — populate
tags on read so the delta sees the true tag state:

```go
	ko.Spec.Tags, err = listTags(
		ctx, rm.sdkapi, rm.metrics,
		string(*r.ko.Status.ACKResourceMetadata.ARN),
	)
	if err != nil {
		return nil, err
	}
```

No Update operation? The tag sync lives in the custom update method instead.
The method must be on `*resourceManager` in `hooks.go`, with exactly this
signature (the generated `sdkUpdate` delegates to it; a wrong signature
fails compilation; `ackcompare` is
`github.com/aws-controllers-k8s/runtime/pkg/compare`):

```go
func (rm *resourceManager) customUpdateAcmeExternalAccountBinding(
	ctx context.Context, desired *resource, latest *resource,
	delta *ackcompare.Delta,
) (*resource, error) {
	if delta.DifferentAt("Spec.Tags") {
		if err := syncTags(ctx, rm.sdkapi, rm.metrics,
			string(*desired.ko.Status.ACKResourceMetadata.ARN),
			desired.ko.Spec.Tags, latest.ko.Spec.Tags); err != nil {
			return nil, err
		}
	}
	if !delta.DifferentExcept("Spec.Tags") {
		return desired, nil
	}
	return nil, ackerr.NewTerminalError(
		errors.New("only tags can be updated for an external account binding"))
}
```

**4b. Normalizing non-echoed fields (`delta_pre_compare`).** When Describe
omits a server-defaulted field (Step 2b row 2), equalize "user said nothing"
with "service reports nothing" so they don't diff forever. Real code
(`templates/hooks/acme_endpoint/delta_pre_compare.go.tpl`, abridged — the
file carries the full explanation):

```go
	// Empty publicCertificateAuthority ({}) means "no restrictions"; the
	// service stores nothing, so Describe omits CertificateAuthority
	// entirely. Treat user-empty and service-absent as equal, and unset
	// allowedKeyAlgorithms as "no opinion". Genuine changes still diff.
	if a.ko.Spec.CertificateAuthority != nil && b.ko.Spec.CertificateAuthority == nil &&
		a.ko.Spec.CertificateAuthority.PublicCertificateAuthority != nil &&
		a.ko.Spec.CertificateAuthority.PublicCertificateAuthority.AllowedKeyAlgorithms == nil {
		b.ko.Spec.CertificateAuthority = a.ko.Spec.CertificateAuthority
	}
```

For the transformed-shape case (Step 2b row 3), reconstruct the spec field
from what the service reports — see
`templates/hooks/acme_domain_validation/sdk_read_one_post_set_output.go.tpl`
(rebuilds `spec.prevalidationOptions` from `PrevalidationDetails`) plus its
`delta_pre_compare.go.tpl` (no-opinion normalization for server-defaulted
sub-fields; without it, partially-specified scopes produced 91 no-op updates
per observation window, each resetting validation).

**4c. State guards for restricted operations (Step 2c).** When an Update is
only valid in some states, mark the resource unsynced with a message and
requeue — don't call the API and don't fail. Endpoint update hook +
`hooks.go` helpers:

```go
	// in sdk_update_pre_build_request.go.tpl, after the tag short-circuit:
	if !endpointActive(latest) {
		updatedRes := rm.concreteResource(desired.DeepCopy())
		updatedRes.SetStatus(latest)
		msg := "Endpoint is in '" + *latest.ko.Status.Status + "' status"
		ackcondition.SetSynced(updatedRes, corev1.ConditionFalse, &msg, nil)
		return updatedRes, requeueWaitUntilCanModify(latest)
	}
```

```go
// in hooks.go:
const StatusActive = "ACTIVE"

func endpointActive(r *resource) bool {
	if r.ko.Status.Status == nil {
		return false
	}
	return *r.ko.Status.Status == StatusActive
}

func requeueWaitUntilCanModify(r *resource) *ackrequeue.RequeueNeededAfter {
	if r.ko.Status.Status == nil {
		return nil
	}
	return ackrequeue.NeededAfter(
		fmt.Errorf("endpoint in '%s' state, cannot be modified until '%s'",
			*r.ko.Status.Status, StatusActive),
		ackrequeue.DefaultRequeueAfterDuration,
	)
}
```

Unit-test the helpers in `hooks_test.go` — transitional windows can be too
short to hit in e2e (endpoint creation settles in ~2 seconds). Hand-written
Go must be gofmt-clean: regeneration runs `gofmt -w`, so an unformatted file
shows up as `verify-code-gen` drift.

**4d. Create-time side effects (`sdk_create_post_set_output`).** For output
only retrievable at create time — the EAB fetches its credentials with a
second API call and writes them into the user-named, **pre-existing** Secret
(the controller patches, never creates, matching `Certificate.exportTo`):

```go
	// abridged from templates/hooks/acme_external_account_binding/
	// sdk_create_post_set_output.go.tpl — post_set_output because it needs
	// the ARN the response mapping just set:
	credResp, err := rm.sdkapi.GetAcmeExternalAccountBindingCredentials(ctx,
		&svcsdk.GetAcmeExternalAccountBindingCredentialsInput{
			AcmeExternalAccountBindingArn: (*string)(ko.Status.ACKResourceMetadata.ARN)})
	// ...
	err = rm.rr.WriteToSecret(ctx, *credResp.MacKey, secretNamespace, secretName, macKeyKey)
```

## Step 5 — Documentation, README, IAM policy

A PR missing these is incomplete regardless of code quality.

`documentation.yaml` — user-facing docs for fields whose SDK docs are thin:

```yaml
resources:
  AcmeEndpoint:
    fields:
      Contact:
        prepend: |
          Whether ACME clients must provide contact information during
          account registration. Valid values: REQUIRED, NOT_REQUIRED.
```

`README.md` — a `### AcmeEndpoint` heading under the resources section with
one paragraph and a minimal manifest:

```yaml
apiVersion: acm.services.k8s.aws/v1alpha1
kind: AcmeEndpoint
metadata:
  name: my-acme-endpoint
spec:
  authorizationBehavior: PRE_APPROVED
  certificateAuthority:
    publicCertificateAuthority: {}
```

`config/iam/recommended-inline-policy` — add the new API actions
(`acm:CreateAcmeEndpoint`, ...). If the resource passes an IAM role to the
service, add `iam:PassRole` conditioned on the service principal
(`"iam:PassedToService": "acm-acme.amazonaws.com"`).

## Step 6 — Generate and verify

From the code-generator checkout (Step 0):

```bash
make build-controller SERVICE=acm
```

Never hand-edit generated files, never use partial `ack-generate`
invocations — the canonical flow also regenerates `helm/crds/`, which CI
checks (a stale helm CRD failed CI on the first ACME PR). Commit everything
it produces, including `apis/v1alpha1/ack-generate-metadata.yaml` — its
build-timestamp fields change every run and CI filters them as noise.
`CONTRIBUTING.md`, `CODE_OF_CONDUCT.md`, `GOVERNANCE.md`, `LICENSE`, and
`NOTICE` are copied from code-generator during this step; don't edit them
here.

To prove the codegen check will pass: commit, regenerate again, and confirm
`git status` shows only metadata timestamps. Also eyeball the generated kind
and type names — initialism handling lives in `aws-controllers-k8s/pkg`, and
a mangled name (`ACMeEndpoint` happened; the fix needed a `pkg` release plus
a code-generator bump) blocks your PR on upstream until fixed, so discover
it now, not in review.

**CI:** the checks (`acm-verify-code-gen`, `acm-kind-e2e`,
`acm-verify-attribution`, ...) are Prow jobs defined in
`aws-controllers-k8s/test-infra`, not in this repo's `.github/workflows/`. A
maintainer must comment `/ok-to-test` on your first PR before they run.
`verify-attribution` regenerates `ATTRIBUTION.md` from `go.mod` and goes red
on any dependency change — expected; it's fixed by the maintainers' release
automation, so don't hand-edit `ATTRIBUTION.md`.

## Step 7 — e2e tests, the way CI runs them

Requirements that came directly from review: cover **create, update, and
delete** (update every mutable field, not just tags); verify against **AWS
as the source of truth**; **poll** for sync instead of sleeping:

```python
# each wait_period is ~60 seconds
assert k8s.wait_on_condition(ref, "ACK.ResourceSynced", "True", wait_periods=3)
aws = acm_client.describe_acme_endpoint(AcmeEndpointArn=arn)["AcmeEndpoint"]
assert aws["Contact"] == "REQUIRED"
# assert_equal_without_ack_tags ignores the services.k8s.aws/* tags the
# controller adds, and compares the user tag set exactly:
tags.assert_equal_without_ack_tags(expected={"team": "platform"},
                                   actual=_aws_resource_tags(acm_client, arn))
```

Test plumbing lives in `test/e2e/`: `conftest.py` provides the `acm_client`
boto3 fixture; manifests go in `resources/` with `$PLACEHOLDER` substitution
via `load_resource(...)`; follow `tests/test_acme.py` for the
fixture-creates/yields/deletes pattern.

Run the suite in a virtualenv built from **`test/e2e/requirements.txt`** —
CI installs exactly those pins. Note boto3 is pinned *transitively* through
the `acktest` git ref in that file: the first ACME kind-e2e run failed with
`'ACM' object has no attribute 'describe_acme_endpoint'` because the
acktest-pinned boto3 predated the new APIs while a locally-upgraded boto3
had masked it. The fix was bumping the acktest ref.

```bash
python3 -m venv /tmp/ci-env
/tmp/ci-env/bin/pip install -r test/e2e/requirements.txt setuptools
# PYTHONPATH=.. makes the `from e2e import ...` imports resolve:
cd test/e2e && PYTHONPATH=.. AWS_REGION=us-east-1 \
  /tmp/ci-env/bin/python -m pytest tests/test_acme.py -v
```

Any AWS dependency the tests need must be bootstrapped in
`service_bootstrap.py` and torn down in `service_cleanup.py` — CI has no
standing infrastructure. See `EABIssuanceRole` in
`test/e2e/bootstrap_resources.py` (an IAM role with a service trust policy,
created per-run) and how the test reads it back:

```python
from e2e.bootstrap_resources import get_bootstrap_resources
role_arn = get_bootstrap_resources().EABRole.arn
```

Gotcha from that work: don't give a bootstrap dataclass field the same name
as its own class — the acktest bootstrapper's type check fails silently and
the resource is never created.

## Step 8 — The spurious-delta sweep (run before opening the PR)

e2e fixtures set every field, so they structurally cannot catch
server-defaulted-field churn. This sweep caught two real bugs that every
e2e run had missed (`contact`: a no-op update on every reconcile of every
bare-bones endpoint; partial `domainScope`: 91 no-op updates that each reset
validation).

Protocol: enumerate spec permutations over the unset/set axis of every
optional field (spec surfaces are small — `AcmeEndpoint` has 24), create
them all against a locally-running controller (Step 0) with
`--log-level=debug`, let them settle, then watch the log for 10+ reconcile
cycles per resource. **Pass = zero deltas with the expected cycle count.**

```bash
#!/bin/bash
# Prereqs: controller running locally with --log-level=debug > $LOG (Step 0);
# permutation manifests in $PERMS (one resource per permutation).
LOG=/tmp/controller-debug.log
PERMS=/tmp/perms.yaml
CTRL_PID=$(pgrep -f "acm-controller --aws-region") || { echo "controller not running"; exit 1; }
kubectl apply -f "$PERMS"
sleep 90                                  # settle: create + late-init
MARK=$(wc -l < "$LOG")
sleep 330                                 # observe 11 cycles at 30s requeue
kill -0 "$CTRL_PID" 2>/dev/null || { echo "FATAL: controller died mid-run"; exit 1; }
for NAME in $(kubectl get acmeendpoints -o name | cut -d/ -f2); do
  CYCLES=$(tail -n +$MARK "$LOG" | grep "\"name\":\"$NAME\"" | grep -c "no difference found")
  DELTAS=$(tail -n +$MARK "$LOG" | grep "\"name\":\"$NAME\"" | grep -c "desired resource state has changed")
  UPDATES=$(tail -n +$MARK "$LOG" | grep "\"name\":\"$NAME\"" | grep -c ">>>> rm.sdkUpdate")
  echo "$NAME cycles=$CYCLES deltas=$DELTAS updates=$UPDATES"
  [ "$CYCLES" -lt 5 ] && echo "  WARN: too few cycles observed — result not trustworthy"
done
kubectl delete -f "$PERMS"
```

The validity checks are not optional: an early sweep reported "all clean"
because the controller had silently died on expired credentials — an idle
controller reports no deltas. Every permutation must show both the expected
cycle count **and** zero deltas.

## Pre-submit checklist

Each item points at the step that shows you how.

- [ ] Environment: sibling code-generator + runtime checkouts working;
      controller runs locally (Step 0)
- [ ] One resource in this PR; SDK bump landed separately (Step 1)
- [ ] Create/Update input shapes audited; `is_immutable` on every field
      absent from the Update input (Step 2a)
- [ ] Describe echo behavior checked per optional field; handling chosen
      from the Step 2b table (Step 4b for the hooks)
- [ ] Update/Delete tested live in each lifecycle state; guards where
      rejected (Steps 2c, 4c)
- [ ] `Create*Input.IdempotencyToken` ignored; status fields mapped with
      `is_read_only` + `from:`; cross-resource ARNs use `references:`;
      no speculative fields — CRD changes are permanent (Step 3)
- [ ] No `late_initialize` on a field the service can omit (Step 3)
- [ ] Terminal codes = user-must-edit-spec only; everything else retries
      (Step 3)
- [ ] Tag support wired, including the no-update-op variant if applicable
      (Step 4a)
- [ ] Read-augmentation hooks in `post_set_output`; helpers unit-tested;
      hand-written Go gofmt-clean (Step 4)
- [ ] `documentation.yaml`, `README.md`, IAM policy updated (Step 5)
- [ ] Regenerated twice via `make build-controller` → only metadata
      timestamps differ; generated names not mangled (Step 6)
- [ ] e2e: create/update/delete, AWS-verified, `ResourceSynced` polling,
      CI-pinned virtualenv, dependencies bootstrapped (Step 7)
- [ ] Spurious-delta sweep: all permutations, zero deltas, validity checks
      passed (Step 8)
