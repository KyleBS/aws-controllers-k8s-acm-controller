	// After creating the EAB, fetch the credentials and store them in the
	// user-specified Secret. The credentials (keyId + macKey) are only available
	// via GetAcmeExternalAccountBindingCredentials. The target Secret must
	// already exist — WriteToSecret patches it (same contract as
	// Certificate.exportTo). If no namespace is specified, the CR's namespace
	// is used.
	if ko.Spec.CredentialsOutput != nil && ko.Status.ACKResourceMetadata != nil && ko.Status.ACKResourceMetadata.ARN != nil {
		credInput := &svcsdk.GetAcmeExternalAccountBindingCredentialsInput{
			AcmeExternalAccountBindingArn: (*string)(ko.Status.ACKResourceMetadata.ARN),
		}
		credResp, credErr := rm.sdkapi.GetAcmeExternalAccountBindingCredentials(ctx, credInput)
		rm.metrics.RecordAPICall("READ_ONE", "GetAcmeExternalAccountBindingCredentials", credErr)
		if credErr != nil {
			return nil, credErr
		}
		// An EAB is only usable with both credentials, so require both.
		if credResp.KeyId == nil || credResp.MacKey == nil {
			return nil, ackerr.NewTerminalError(fmt.Errorf("GetAcmeExternalAccountBindingCredentials did not return both keyId and macKey"))
		}

		// Surface the (non-sensitive) key identifier in status so ACME clients
		// can reference it when registering an account.
		ko.Status.KeyID = credResp.KeyId

		secretNamespace := ko.Spec.CredentialsOutput.Namespace
		if secretNamespace == "" {
			secretNamespace = ko.Namespace
		}
		secretName := ko.Spec.CredentialsOutput.Name

		// Write the sensitive macKey to the user-specified key (following the
		// same pattern as Certificate.exportTo, where the primary value uses
		// the user-provided key). The non-sensitive keyId is written to a
		// fixed "keyId" key for convenience; it is also available in status.
		macKeyKey := ko.Spec.CredentialsOutput.Key
		if macKeyKey == "" {
			macKeyKey = "macKey"
		}
		writeErr := rm.rr.WriteToSecret(ctx, *credResp.MacKey, secretNamespace, secretName, macKeyKey)
		rm.metrics.RecordAPICall("PATCH", "WriteEABCredentialsSecret", writeErr)
		if writeErr != nil {
			return nil, writeErr
		}
		writeErr = rm.rr.WriteToSecret(ctx, *credResp.KeyId, secretNamespace, secretName, "keyId")
		rm.metrics.RecordAPICall("PATCH", "WriteEABCredentialsSecret", writeErr)
		if writeErr != nil {
			return nil, writeErr
		}
	}
