// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package factory_test

import (
	"context"

	agentlib "github.com/bborbe/agent"
	"github.com/bborbe/cqrs/base"
	kafkamocks "github.com/bborbe/kafka/mocks"
	libtime "github.com/bborbe/time"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/bborbe/agent-claude/pkg/factory"
)

// This is a golden-master characterization test. It pins the exact Kafka
// topic name produced by CreateKafkaResultDeliverer for a given TopicPrefix,
// proving the migration from base.Branch to base.TopicPrefix preserves the
// existing dev/prod topic names and adds a clean unprefixed case.
var _ = Describe("CreateKafkaResultDeliverer topic naming (golden master)", func() {
	var (
		ctx             context.Context
		fakeProducer    *kafkamocks.KafkaSyncProducer
		currentDateTime libtime.CurrentDateTimeGetter
	)

	BeforeEach(func() {
		ctx = context.Background()
		fakeProducer = &kafkamocks.KafkaSyncProducer{}
		fakeProducer.SendMessageReturns(0, 0, nil)
		currentDateTime = libtime.NewCurrentDateTime()
	})

	deliver := func(topicPrefix base.TopicPrefix) string {
		deliverer := factory.CreateKafkaResultDeliverer(
			fakeProducer,
			topicPrefix,
			agentlib.TaskIdentifier("task-1"),
			"# Task\n\nbody",
			currentDateTime,
		)
		Expect(deliverer.DeliverResult(ctx, agentlib.AgentResultInfo{
			Status: agentlib.AgentStatusDone,
			Output: "# Task\n\nbody",
		})).To(Succeed())
		Expect(fakeProducer.SendMessageCallCount()).To(Equal(1))
		_, msg := fakeProducer.SendMessageArgsForCall(0)
		return msg.Topic
	}

	It("prefixes with 'develop-' for TopicPrefix(\"develop\")", func() {
		Expect(deliver(base.TopicPrefix("develop"))).To(Equal("develop-agent-task-v1-request"))
	})

	It("prefixes with 'master-' for TopicPrefix(\"master\")", func() {
		Expect(deliver(base.TopicPrefix("master"))).To(Equal("master-agent-task-v1-request"))
	})

	It("is unprefixed for an empty TopicPrefix", func() {
		Expect(deliver(base.TopicPrefix(""))).To(Equal("agent-task-v1-request"))
	})
})
