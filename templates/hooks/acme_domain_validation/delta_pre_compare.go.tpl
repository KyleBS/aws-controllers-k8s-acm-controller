	// The service defaults any domainScope option the user leaves unset and
	// reports the effective configuration in PrevalidationDetails (which the
	// read hook mirrors back into spec.prevalidationOptions of the latest
	// resource). Treat unset fields in the desired spec as "no opinion" and
	// adopt the reported values, so a partially-specified domainScope does
	// not diff forever against the server-defaulted configuration (which
	// would trigger a no-op UpdateAcmeDomainValidation every reconcile and
	// reset validation each time). A genuine change to any specified field
	// still produces a diff.
	if a.ko.Spec.PrevalidationOptions != nil && b.ko.Spec.PrevalidationOptions != nil &&
		a.ko.Spec.PrevalidationOptions.DNSPrevalidation != nil &&
		b.ko.Spec.PrevalidationOptions.DNSPrevalidation != nil {
		desired := a.ko.Spec.PrevalidationOptions.DNSPrevalidation
		latest := b.ko.Spec.PrevalidationOptions.DNSPrevalidation
		if desired.HostedZoneID == nil {
			desired.HostedZoneID = latest.HostedZoneID
		}
		if desired.DomainScope == nil {
			desired.DomainScope = latest.DomainScope
		} else if latest.DomainScope != nil {
			if desired.DomainScope.ExactDomain == nil {
				desired.DomainScope.ExactDomain = latest.DomainScope.ExactDomain
			}
			if desired.DomainScope.Subdomains == nil {
				desired.DomainScope.Subdomains = latest.DomainScope.Subdomains
			}
			if desired.DomainScope.Wildcards == nil {
				desired.DomainScope.Wildcards = latest.DomainScope.Wildcards
			}
		}
	}
