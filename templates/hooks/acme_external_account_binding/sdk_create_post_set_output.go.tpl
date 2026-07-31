	// Record the credentials: the key identifier in status, and the secret MAC key in
	// the Secret named by spec.credentialsOutput. They are only available from
	// GetAcmeExternalAccountBindingCredentials, so this cannot be part of the create
	// response handling.
	//
	// Failures are returned as a requeue rather than as an AWS error, and the resource is
	// returned rather than nil: the binding already exists in ACM at this point, so
	// discarding it (nil) or surfacing an AWS error (which makes the runtime mark the
	// resource unmanaged and drop the finalizer) would orphan a live credential.
	if err := rm.storeEABCredentials(ctx, ko); err != nil {
		return &resource{ko}, err
	}
