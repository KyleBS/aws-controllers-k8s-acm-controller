	ko.Spec.Tags, err = listTags(
		ctx, rm.sdkapi, rm.metrics,
		string(*r.ko.Status.ACKResourceMetadata.ARN),
	)
	if err != nil {
		return nil, err
	}

	// Repair a binding whose credentials never reached the Secret or status — the create
	// pass can fail after the binding exists (a Secret that does not exist yet, a denied
	// credentials call, a throttle), and a user can add spec.credentialsOutput or empty the
	// Secret later. needsCredentials reads the Secret rather than trusting status, so the
	// steady state costs one Kubernetes read and no AWS call.
	//
	// NOT while the resource is being deleted. The runtime's delete path calls ReadOne
	// FIRST and abandons the delete on any error that is not NotFound, so a repair that
	// fails here would stop DeleteAcmeExternalAccountBinding from ever being called: the CR
	// would sit in Terminating for ever (taking its namespace with it) and the binding would
	// be left live in ACM. That happens for real whenever the Secret is removed before the
	// CR — GitOps pruning, or a namespace deletion, where the Secret has no finalizer and
	// goes immediately while this resource waits on ours. There is nothing to repair on a
	// resource that is going away.
	if r.ko.DeletionTimestamp.IsZero() {
		needs, err := rm.needsCredentials(ctx, ko)
		if err != nil {
			// The Secret cannot be read at all — a non-Opaque type, or a cross-namespace
			// reference the operator has not enabled. Report it, rather than treating it as
			// "not stored" and re-fetching the credential from AWS on every pass.
			return &resource{ko}, err
		}
		if needs {
			if err := rm.storeEABCredentials(ctx, ko); err != nil {
				return &resource{ko}, err
			}
		}
	}
