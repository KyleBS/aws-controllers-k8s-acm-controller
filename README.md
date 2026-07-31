# ACK service controller for AWS Certificate Manager

This repository contains source code for the AWS Controllers for Kubernetes
(ACK) service controller for ACM.

Please [log issues][ack-issues] and feedback on the main AWS Controllers for
Kubernetes Github project.

[ack-issues]: https://github.com/aws/aws-controllers-k8s/issues

## Getting Started

### Installation Instructions
Learn more about [installing ACK service controller for AWS Certificate Manager](https://docs.aws.amazon.com/acm/latest/userguide/exportable-certificates-kubernetes.html).

### Pricing
The ACK service controller for AWS Certificate Manager is free of charge. If you issue an [exportable public certificate](https://docs.aws.amazon.com/acm/latest/userguide/acm-exportable-certificates.html) with AWS Certificate Manager, there is a charge at certificate issuance and again when the certificate renews. Learn more about [AWS Certificate Manager Pricing](https://aws.amazon.com/certificate-manager/pricing/).

[samples]: https://github.com/aws-controllers-k8s/acmpca-controller/tree/main/samples

### Kubernetes Secrets
The ACK service controller for AWS Certificate Manager uses Kubernetes TLS Secrets to store the certificate chain and decrypted private key of the exported ACM certificate. Users are expected to create Secrets before creating Certificate resources. As these resources are created, the Secrets' `tls.crt` will be injected with the base64-encoded certificate and `tls.key` will be injected with the base64-encoded private key associated with the certificate. Users are responsible for deleting Secrets.

In addition, after a certificate is successfully renewed by ACM, the ACK service controller for AWS Certificate Manager will automatically export the renewed certificate again so that the Kubernetes TLS Secret `exportTo` contains the certificate data and private key data of the renewed certificate.


#### Export Certificate
To export an ACM certificate to a Kubernetes TLS Secret, users must specify the namespace and the name of the Secret using the `exportTo` field of the Certificate resource, as shown below.

##### Exporting an exportable ACM public certificate
```
apiVersion: v1
kind: Secret
type: kubernetes.io/tls
metadata:
  name: exported-cert-secret
  namespace: demo-app
data:
  tls.crt: ""
  tls.key: ""
---
apiVersion: acm.services.k8s.aws/v1alpha1
kind: Certificate
metadata:
  name: exportable-public-cert
  namespace: demo-app
spec:
  domainName: my.domain.com
  options:
    certificateTransparencyLoggingPreference: ENABLED
  exportTo:
    namespace: demo-app
    name: exported-cert-secret
    key: tls.crt
...
```

##### Exporting an ACM private certificate
```
apiVersion: v1
kind: Secret
type: kubernetes.io/tls
metadata:
  name: exported-cert-secret
  namespace: demo-app-2
data:
  tls.crt: ""
  tls.key: ""
---
apiVersion: acm.services.k8s.aws/v1alpha1
kind: Certificate
metadata:
  name: exportable-private-cert
  namespace: demo-app-2
spec:
  domainName: my.domain.com
  certificateAuthorityARN: arn:aws:acm-pca:{$REGION}:{$AWS_ACCOUNT}:certificate-authority/12345678-1234-1234-1234-123456789012
  keyAlgorithm: EC_secp384r1
  exportTo:
    namespace: demo-app-2
    name: exported-cert-secret
    key: tls.crt
```
If you are issuing a privately trusted certificate, please also consider using this cert-manager plugin: https://github.com/cert-manager/aws-privateca-issuer/.

## Contributing

We welcome community contributions and pull requests.

See our [contribution guide](/CONTRIBUTING.md) for more information on how to
report issues, set up a development environment, and submit code.

We adhere to the [Amazon Open Source Code of Conduct][coc].

You can also learn more about our [Governance](/GOVERNANCE.md) structure.

[coc]: https://aws.github.io/code-of-conduct

## License

This project is [licensed](/LICENSE) under the Apache-2.0 License.

## ACME Resources

This controller also manages ACME (Automatic Certificate Management Environment) resources for ACM. ACME enables standards-based certificate automation using any ACME-compatible client (cert-manager, Certbot, acme.sh, etc.).

### AcmeEndpoint

Creates a managed ACME server endpoint with a unique URL for certificate automation.

```yaml
apiVersion: acm.services.k8s.aws/v1alpha1
kind: AcmeEndpoint
metadata:
  name: my-acme-endpoint
spec:
  authorizationBehavior: PRE_APPROVED
  certificateAuthority:
    publicCertificateAuthority: {}
  contact: NOT_REQUIRED
```

After creation, `status.endpointURL` contains the ACME directory URL (e.g., `https://acm-acme-enroll.us-east-1.api.aws/<id>/directory`).

### AcmeDomainValidation

Authorizes an ACME endpoint to issue certificates for specific domain names.

```yaml
apiVersion: acm.services.k8s.aws/v1alpha1
kind: AcmeDomainValidation
metadata:
  name: my-domain-validation
spec:
  acmeEndpointARN: arn:aws:acm:us-east-1:123456789012:acme-endpoint/abc-123
  domainName: example.com
  prevalidationOptions:
    dnsPrevalidation:
      hostedZoneId: Z0123456789ABC
      domainScope:
        exactDomain: ENABLED
        subdomains: ENABLED
        wildcards: ENABLED
```

### AcmeExternalAccountBinding

Creates EAB credentials that authorize ACME clients to register accounts with the endpoint.

You must create the target Secret before creating the resource — the controller
populates an existing Secret rather than creating one (the same pattern as the
`Certificate` resource's `exportTo` field).

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: my-eab-credentials
  namespace: default
type: Opaque
---
apiVersion: acm.services.k8s.aws/v1alpha1
kind: AcmeExternalAccountBinding
metadata:
  name: my-eab
spec:
  acmeEndpointARN: arn:aws:acm:us-east-1:123456789012:acme-endpoint/abc-123
  roleARN: arn:aws:iam::123456789012:role/AcmeAccountRole
  credentialsOutput:
    namespace: default
    name: my-eab-credentials
    key: macKey
```

After creation, the controller writes the sensitive `macKey` into the specified Kubernetes Secret under the given `key`, and also writes the `keyId` under a fixed `keyId` key (unless `key` is itself `keyId`, in which case the `macKey` wins and the identifier is available in `status.keyID`). The Secret must already exist. If `namespace` is omitted, the resource's namespace is used.

The IAM role named by `roleARN` is assumed by the ACME service to issue certificates, so
its trust policy must grant **`acm-acme.amazonaws.com`** — not `acm.amazonaws.com` — access
to `sts:AssumeRole`, `sts:SetSourceIdentity` and `sts:TagSession`. All three are required;
a role granting only `sts:AssumeRole` is rejected when the binding is created:

```json
{"Version": "2012-10-17", "Statement": [{
  "Effect": "Allow",
  "Principal": {"Service": "acm-acme.amazonaws.com"},
  "Action": ["sts:AssumeRole", "sts:SetSourceIdentity", "sts:TagSession"]}]}
```

The role also needs `acm:RequestCertificate`, `acm:DescribeCertificate` and
`acm:GetCertificate`.

Note the two service principals are different, and both values matter:

| Where | Value |
|---|---|
| the issuance role's **trust policy** | `acm-acme.amazonaws.com` |
| the controller's `iam:PassRole` **condition** (`iam:PassedToService`) | `acm.amazonaws.com` |

Verified against the service: a trust policy naming `acm.amazonaws.com` is rejected when the
binding is created, and a `PassRole` condition naming only `acm-acme.amazonaws.com` denies the
create call. `config/iam/recommended-inline-policy` uses the correct value.

The non-sensitive key identifier is also surfaced in `status.keyID`, so ACME clients can reference it directly without reading the Secret. The sensitive `macKey` is only ever written to the Secret.
