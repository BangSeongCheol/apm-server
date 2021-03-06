// Licensed to Elasticsearch B.V. under one or more contributor
// license agreements. See the NOTICE file distributed with
// this work for additional information regarding copyright
// ownership. Elasticsearch B.V. licenses this file to you under
// the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing,
// software distributed under the License is distributed on an
// "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
// KIND, either express or implied.  See the License for the
// specific language governing permissions and limitations
// under the License.

package systemtest_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"go.elastic.co/apm"

	"github.com/elastic/apm-server/systemtest"
	"github.com/elastic/apm-server/systemtest/apmservertest"
	"github.com/elastic/apm-server/systemtest/estest"
)

func TestTransactionAggregation(t *testing.T) {
	systemtest.CleanupElasticsearch(t)
	srv := apmservertest.NewUnstartedServer(t)
	srv.Config.Monitoring = &apmservertest.MonitoringConfig{
		Enabled:       true,
		MetricsPeriod: 100 * time.Millisecond,
		StatePeriod:   100 * time.Millisecond,
	}
	srv.Config.Aggregation = &apmservertest.AggregationConfig{
		Transactions: &apmservertest.TransactionAggregationConfig{
			Enabled:  true,
			Interval: time.Second,
		},
	}
	srv.Config.Sampling = &apmservertest.SamplingConfig{
		// Drop unsampled transaction events, to show
		// that we aggregate before they are dropped.
		KeepUnsampled: false,
	}
	err := srv.Start()
	require.NoError(t, err)

	// Send some transactions to the server to be aggregated.
	tracer := srv.Tracer()
	tx := tracer.StartTransaction("name", "backend")
	req, _ := http.NewRequest("GET", "/", nil)
	tx.Context.SetHTTPRequest(req)
	tx.Duration = time.Second
	tx.End()
	tracer.Flush(nil)

	result := systemtest.Elasticsearch.ExpectDocs(t, "apm-*",
		estest.ExistsQuery{Field: "transaction.duration.histogram"},
	)
	systemtest.ApproveEvents(t, t.Name(), result.Hits.Hits, "@timestamp")

	// Make sure apm-server.aggregation.txmetrics metrics are published. Metric values are unit tested.
	doc := getBeatsMonitoringStats(t, srv, nil)
	assert.True(t, gjson.GetBytes(doc.RawSource, "beats_stats.metrics.apm-server.aggregation.txmetrics").Exists())
}

func TestTransactionAggregationShutdown(t *testing.T) {
	systemtest.CleanupElasticsearch(t)
	srv := apmservertest.NewUnstartedServer(t)
	srv.Config.Aggregation = &apmservertest.AggregationConfig{
		Transactions: &apmservertest.TransactionAggregationConfig{
			Enabled: true,
			// Set aggregation_interval to something that would cause
			// a timeout if we were to wait that long. The server
			// should flush metrics on shutdown without waiting for
			// the configured interval.
			Interval: time.Minute,
		},
	}
	err := srv.Start()
	require.NoError(t, err)

	// Send a transaction to the server to be aggregated.
	tracer := srv.Tracer()
	tx := tracer.StartTransaction("name", "type")
	tx.Duration = time.Second
	tx.End()
	tracer.Flush(nil)

	// Stop server to ensure metrics are flushed on shutdown.
	assert.NoError(t, srv.Close())

	result := systemtest.Elasticsearch.ExpectDocs(t, "apm-*",
		estest.ExistsQuery{Field: "transaction.duration.histogram"},
	)
	systemtest.ApproveEvents(t, t.Name(), result.Hits.Hits, "@timestamp")
}

func TestServiceDestinationAggregation(t *testing.T) {
	systemtest.CleanupElasticsearch(t)
	srv := apmservertest.NewUnstartedServer(t)
	srv.Config.Aggregation = &apmservertest.AggregationConfig{
		ServiceDestinations: &apmservertest.ServiceDestinationAggregationConfig{
			Enabled:  true,
			Interval: time.Second,
		},
	}
	err := srv.Start()
	require.NoError(t, err)

	// Send spans to the server to be aggregated.
	tracer := srv.Tracer()
	tx := tracer.StartTransaction("name", "type")
	for i := 0; i < 5; i++ {
		span := tx.StartSpan("name", "type", nil)
		span.Context.SetDestinationService(apm.DestinationServiceSpanContext{
			Name:     "name",
			Resource: "resource",
		})
		span.Duration = time.Second
		span.End()
	}
	tx.End()
	tracer.Flush(nil)

	result := systemtest.Elasticsearch.ExpectDocs(t, "apm-*",
		estest.ExistsQuery{Field: "span.destination.service.response_time.count"},
	)
	systemtest.ApproveEvents(t, t.Name(), result.Hits.Hits, "@timestamp")
}
