// Copyright Amazon.com Inc. or its affiliates. All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License"). You may
// not use this file except in compliance with the License. A copy of the
// License is located at
//
//     http://aws.amazon.com/apache2.0/
//
// or in the "license" file accompanying this file. This file is distributed
// on an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either
// express or implied. See the License for the specific language governing
// permissions and limitations under the License.

package tags

import (
	"context"

	"github.com/aws-controllers-k8s/acm-controller/apis/v1alpha1"
	ackrtlog "github.com/aws-controllers-k8s/runtime/pkg/runtime/log"

	svcsdk "github.com/aws/aws-sdk-go-v2/service/acm"
	svcsdktypes "github.com/aws/aws-sdk-go-v2/service/acm/types"
)

// resourceTagsClient is the subset of the ACM API used to manage tags on
// resources that use the standardized TagResource/UntagResource/
// ListTagsForResource operations (i.e. everything other than Certificate,
// which uses the older *TagsToCertificate operations).
type resourceTagsClient interface {
	TagResource(context.Context, *svcsdk.TagResourceInput, ...func(*svcsdk.Options)) (*svcsdk.TagResourceOutput, error)
	UntagResource(context.Context, *svcsdk.UntagResourceInput, ...func(*svcsdk.Options)) (*svcsdk.UntagResourceOutput, error)
	ListTagsForResource(context.Context, *svcsdk.ListTagsForResourceInput, ...func(*svcsdk.Options)) (*svcsdk.ListTagsForResourceOutput, error)
}

// SyncResourceTags examines the Tags in the supplied resource and calls the
// TagResource and UntagResource APIs to ensure that the set of associated Tags
// stays in sync with the desired Spec.Tags. It is the standardized-tagging
// analogue of SyncTags (which targets the Certificate-specific tag API).
func SyncResourceTags(
	ctx context.Context,
	client resourceTagsClient,
	mr metricsRecorder,
	resourceARN string,
	aTags []*v1alpha1.Tag,
	bTags []*v1alpha1.Tag,
) (err error) {
	rlog := ackrtlog.FromContext(ctx)
	exit := rlog.Trace("rm.syncResourceTags")
	defer func() { exit(err) }()

	desiredTags := map[string]*string{}
	for _, t := range aTags {
		desiredTags[*t.Key] = t.Value
	}
	existingTags := map[string]*string{}
	for _, t := range bTags {
		existingTags[*t.Key] = t.Value
	}

	toAdd := map[string]*string{}
	toDeleteKeys := []string{}

	for k, v := range desiredTags {
		if ev, found := existingTags[k]; !found || !equalStrPtr(ev, v) {
			toAdd[k] = v
		}
	}
	for k := range existingTags {
		if _, found := desiredTags[k]; !found {
			toDeleteKeys = append(toDeleteKeys, k)
		}
	}

	if len(toDeleteKeys) > 0 {
		for _, k := range toDeleteKeys {
			rlog.Debug("removing tag from resource", "key", k)
		}
		if err = untagResource(ctx, client, mr, resourceARN, toDeleteKeys); err != nil {
			return err
		}
	}
	if len(toAdd) > 0 {
		for k := range toAdd {
			rlog.Debug("adding tag to resource", "key", k)
		}
		if err = tagResource(ctx, client, mr, resourceARN, toAdd); err != nil {
			return err
		}
	}
	return nil
}

// tagResource adds the supplied tags to the supplied resource ARN.
func tagResource(
	ctx context.Context,
	client resourceTagsClient,
	mr metricsRecorder,
	resourceARN string,
	tags map[string]*string,
) (err error) {
	rlog := ackrtlog.FromContext(ctx)
	exit := rlog.Trace("rm.tagResource")
	defer func() { exit(err) }()

	sdkTags := []svcsdktypes.Tag{}
	for k, v := range tags {
		k := k
		sdkTags = append(sdkTags, svcsdktypes.Tag{Key: &k, Value: v})
	}

	_, err = client.TagResource(ctx, &svcsdk.TagResourceInput{
		ResourceArn: &resourceARN,
		Tags:        sdkTags,
	})
	mr.RecordAPICall("UPDATE", "TagResource", err)
	return err
}

// untagResource removes the supplied tag keys from the supplied resource ARN.
func untagResource(
	ctx context.Context,
	client resourceTagsClient,
	mr metricsRecorder,
	resourceARN string,
	tagKeys []string,
) (err error) {
	rlog := ackrtlog.FromContext(ctx)
	exit := rlog.Trace("rm.untagResource")
	defer func() { exit(err) }()

	_, err = client.UntagResource(ctx, &svcsdk.UntagResourceInput{
		ResourceArn: &resourceARN,
		TagKeys:     tagKeys,
	})
	mr.RecordAPICall("UPDATE", "UntagResource", err)
	return err
}

// ListResourceTags queries the list of tags associated with the supplied
// resource ARN via the standardized ListTagsForResource API.
func ListResourceTags(
	ctx context.Context,
	client resourceTagsClient,
	mr metricsRecorder,
	resourceARN string,
) ([]*v1alpha1.Tag, error) {
	var err error
	rlog := ackrtlog.FromContext(ctx)
	exit := rlog.Trace("rm.listResourceTags")
	defer func() { exit(err) }()

	var out *svcsdk.ListTagsForResourceOutput
	out, err = client.ListTagsForResource(ctx, &svcsdk.ListTagsForResourceInput{
		ResourceArn: &resourceARN,
	})
	mr.RecordAPICall("GET", "ListTagsForResource", err)
	if err != nil {
		return nil, err
	}
	return resourceTagsFromSDKTags(out.Tags), nil
}

// equalStrPtr reports whether two *string values are equal, treating nil as
// distinct from the empty string.
func equalStrPtr(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}
