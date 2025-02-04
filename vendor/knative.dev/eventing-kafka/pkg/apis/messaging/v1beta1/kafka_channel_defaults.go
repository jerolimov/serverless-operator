/*
Copyright 2020 The Knative Authors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1beta1

import (
	"context"

	"knative.dev/eventing-kafka/pkg/common/constants"
	"knative.dev/eventing/pkg/apis/messaging"
)

func (c *KafkaChannel) SetDefaults(ctx context.Context) {
	// Set the duck subscription to the stored version of the duck
	// we support. Reason for this is that the stored version will
	// not get a chance to get modified, but for newer versions
	// conversion webhook will be able to take a crack at it and
	// can modify it to match the duck shape.
	if c.Annotations == nil {
		c.Annotations = make(map[string]string)
	}

	if _, ok := c.Annotations[messaging.SubscribableDuckVersionAnnotation]; !ok {
		c.Annotations[messaging.SubscribableDuckVersionAnnotation] = "v1"
	}

	c.Spec.SetDefaults(ctx)
}

func (cs *KafkaChannelSpec) SetDefaults(ctx context.Context) {
	if cs.NumPartitions == 0 {
		cs.NumPartitions = constants.DefaultNumPartitions
	}
	if cs.ReplicationFactor == 0 {
		cs.ReplicationFactor = constants.DefaultReplicationFactor
	}
}
