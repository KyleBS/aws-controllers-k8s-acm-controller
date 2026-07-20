	// An empty spec.certificateAuthority.publicCertificateAuthority ({})
	// means "issue from the public CA with no key algorithm restrictions".
	// The service stores nothing for it, so DescribeAcmeEndpoint omits
	// CertificateAuthority from its response entirely: absence on the AWS
	// side means "no restrictions", not missing data. The two
	// normalizations below reconcile that representation difference; a
	// genuine change to allowedKeyAlgorithms still produces a diff and
	// reconciles via UpdateAcmeEndpoint.

	// The user specified an empty publicCertificateAuthority and AWS
	// reports nothing: these mean the same thing, so mirror the desired
	// value into latest to suppress the diff (without this, {} vs nil
	// would fire a no-op UpdateAcmeEndpoint on every reconcile, forever).
	if a.ko.Spec.CertificateAuthority != nil && b.ko.Spec.CertificateAuthority == nil &&
		a.ko.Spec.CertificateAuthority.PublicCertificateAuthority != nil &&
		a.ko.Spec.CertificateAuthority.PublicCertificateAuthority.AllowedKeyAlgorithms == nil {
		b.ko.Spec.CertificateAuthority = a.ko.Spec.CertificateAuthority
	}

	// The user left allowedKeyAlgorithms unset but AWS reports values:
	// treat unset as "no opinion" and adopt what AWS reports, mirroring
	// late-initialization semantics without requiring the response to
	// always carry the field.
	if a.ko.Spec.CertificateAuthority != nil && b.ko.Spec.CertificateAuthority != nil &&
		a.ko.Spec.CertificateAuthority.PublicCertificateAuthority != nil &&
		b.ko.Spec.CertificateAuthority.PublicCertificateAuthority != nil &&
		a.ko.Spec.CertificateAuthority.PublicCertificateAuthority.AllowedKeyAlgorithms == nil {
		a.ko.Spec.CertificateAuthority.PublicCertificateAuthority.AllowedKeyAlgorithms =
			b.ko.Spec.CertificateAuthority.PublicCertificateAuthority.AllowedKeyAlgorithms
	}
