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
	// UpdateAcmeDomainValidation is rejected while validation is in progress
	// (VALIDATING). Mark the resource as not synced with a message and
	// requeue until validation settles.
	if !domainValidationModifiable(latest) {
		updatedRes := rm.concreteResource(desired.DeepCopy())
		updatedRes.SetStatus(latest)
		msg := "Domain validation is in '" + *latest.ko.Status.Status + "' status"
		ackcondition.SetSynced(updatedRes, corev1.ConditionFalse, &msg, nil)
		return updatedRes, requeueWaitUntilCanModify(latest)
	}
