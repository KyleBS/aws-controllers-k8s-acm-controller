	ko.Spec.Tags, err = listTags(
		ctx, rm.sdkapi, rm.metrics,
		string(*r.ko.Status.ACKResourceMetadata.ARN),
	)
	if err != nil {
		return nil, err
	}
	// DescribeAcmeDomainValidation does not echo the PrevalidationOptions
	// input; it reports the effective prevalidation state in
	// PrevalidationDetails. Reconstruct spec.prevalidationOptions from those
	// details so the delta compares the actual service state: a user edit to
	// prevalidationOptions then produces a real diff and reconciles via
	// UpdateAcmeDomainValidation, while an unchanged spec produces none.
	if ko.Status.PrevalidationDetails != nil &&
		ko.Status.PrevalidationDetails.DNSPrevalidation != nil {
		details := ko.Status.PrevalidationDetails.DNSPrevalidation
		ko.Spec.PrevalidationOptions = &svcapitypes.PrevalidationOptions{
			DNSPrevalidation: &svcapitypes.DNSPrevalidationOptions{
				DomainScope:  details.DomainScope,
				HostedZoneID: details.HostedZoneID,
			},
		}
	}
