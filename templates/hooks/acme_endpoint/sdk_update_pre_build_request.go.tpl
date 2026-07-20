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
	// UpdateAcmeEndpoint is only valid against an ACTIVE endpoint. Mark the
	// resource as not synced with a message and requeue until it can be
	// modified.
	if !endpointActive(latest) {
		updatedRes := rm.concreteResource(desired.DeepCopy())
		updatedRes.SetStatus(latest)
		msg := "Endpoint is in '" + *latest.ko.Status.Status + "' status"
		ackcondition.SetSynced(updatedRes, corev1.ConditionFalse, &msg, nil)
		return updatedRes, requeueWaitUntilCanModify(latest)
	}
