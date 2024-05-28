package otlp

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path"
	"testing"
	"time"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	v1export "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	"google.golang.org/protobuf/proto"

	"github.com/atlassian/gostatsd"
	"github.com/atlassian/gostatsd/internal/fixtures"
	"github.com/atlassian/gostatsd/pkg/transport"
)

type TestingWriter struct {
	tb testing.TB
}

func (tw TestingWriter) Write(p []byte) (int, error) {
	tw.tb.Log(string(p))
	return len(p), nil
}

func TestNewBackend(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		file   string
		errVal string
	}{
		{
			name:   "Correctly Configured",
			file:   "minimal.toml",
			errVal: "",
		},
		{
			name:   "All configuration options set",
			file:   "all-options.toml",
			errVal: "",
		},
		{
			name:   "No configuration set",
			file:   "empty.toml",
			errVal: "no endpoint defined",
		},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			logger := fixtures.NewTestLogger(t)

			v := viper.New()
			v.SetConfigFile(path.Join("testdata", tc.file))
			require.NoError(t, v.ReadInConfig(), "Must not error reading config")

			bk, err := NewClientFromViper(
				v,
				logger,
				transport.NewTransportPool(
					logger,
					v,
				),
			)
			if tc.errVal != "" {
				assert.Nil(t, bk, "Must have a nil backend returned")
				assert.EqualError(t, err, tc.errVal, "Must match the expected error")
			} else {
				assert.NotNil(t, bk, "Must not return a nil backend")
				assert.NoError(t, err, "Must not return an error")
			}
		})
	}
}

func TestBackendSendAsyncMetrics(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name             string
		enableHistograms bool
		mm               *gostatsd.MetricMap
		handler          http.HandlerFunc
		validate         func(t *testing.T) func(errs []error)
	}{
		{
			name: "trivial example",
			mm:   gostatsd.NewMetricMap(false),
			handler: func(w http.ResponseWriter, r *http.Request) {
				// Do nothing
			},
			validate: func(t *testing.T) func(errs []error) {
				return func(errs []error) {
					assert.Len(t, errs, 0, "Must not returned any error")
				}
			},
		},
		{
			name: "Failed non compliant resposne",
			mm:   gostatsd.NewMetricMap(false),
			handler: func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, "invalid data", http.StatusBadGateway)
			},
			validate: func(t *testing.T) func(errs []error) {
				return func(errs []error) {
					if !assert.Len(t, errs, 1, "Must have one error") {
						return
					}
					assert.ErrorIs(t, errs[0], proto.Error, "Must match the expected error")
				}
			},
		},
		{
			name: "Dropped data",
			mm:   gostatsd.NewMetricMap(false),
			handler: func(w http.ResponseWriter, r *http.Request) {
				resp := v1export.ExportMetricsServiceResponse{
					PartialSuccess: &v1export.ExportMetricsPartialSuccess{
						ErrorMessage:       "Failed to find auth token",
						RejectedDataPoints: 100,
					},
				}
				buf, err := proto.Marshal(&resp)
				require.NoError(t, err, "Must not fail to marshal values")
				w.Write(buf)
				w.WriteHeader(http.StatusBadRequest)
			},
			validate: func(t *testing.T) func(errs []error) {
				return func(errs []error) {
					if !assert.Len(t, errs, 2, "Must have one error") {
						return
					}
					assert.EqualError(t, errs[0], "dataloss: dropped 100 metrics")
					assert.EqualError(t, errs[1], "failed to send metrics: Failed to find auth token")
				}
			},
		},
		{
			name: "valid metric data",
			mm: func() *gostatsd.MetricMap {
				mm := gostatsd.NewMetricMap(false)
				mm.Receive(&gostatsd.Metric{
					Name:      "my-metric",
					Value:     100.0,
					Rate:      1,
					Tags:      gostatsd.Tags{"service.name:my-awesome-service"},
					Timestamp: gostatsd.Nanotime(time.Unix(100, 0).UnixNano()),
					Type:      gostatsd.COUNTER,
				})
				mm.Receive(&gostatsd.Metric{
					Name:      "my-metric",
					Value:     100.0,
					Rate:      1,
					Tags:      gostatsd.Tags{"service.name:my-awesome-service"},
					Timestamp: gostatsd.Nanotime(time.Unix(100, 0).UnixNano()),
					Type:      gostatsd.GAUGE,
				})
				mm.Receive(&gostatsd.Metric{
					Name:      "my-metric",
					Value:     100.0,
					Rate:      1,
					Tags:      gostatsd.Tags{"service.name:my-awesome-service"},
					Timestamp: gostatsd.Nanotime(time.Unix(100, 0).UnixNano()),
					Type:      gostatsd.SET,
				})
				mm.Receive(&gostatsd.Metric{
					Name:      "my-metric",
					Value:     100.0,
					Rate:      1,
					Tags:      gostatsd.Tags{"service.name:my-awesome-service"},
					Timestamp: gostatsd.Nanotime(time.Unix(100, 0).UnixNano()),
					Type:      gostatsd.TIMER,
				})
				return mm
			}(),
			handler: func(w http.ResponseWriter, r *http.Request) {
				// Do nothing
			},
			validate: func(t *testing.T) func(errs []error) {
				return func(errs []error) {
					if !assert.Len(t, errs, 0, "Must not error") {
						return
					}
				}
			},
		},
		{
			name: "valid metric data with histogram conversion",
			mm: func() *gostatsd.MetricMap {
				mm := gostatsd.NewMetricMap(false)
				mm.Receive(&gostatsd.Metric{
					Name:      "my-metric",
					Value:     100.0,
					Rate:      1,
					Tags:      gostatsd.Tags{"service.name:my-awesome-service"},
					Timestamp: gostatsd.Nanotime(time.Unix(100, 0).UnixNano()),
					Type:      gostatsd.COUNTER,
				})
				mm.Receive(&gostatsd.Metric{
					Name:      "my-metric",
					Value:     100.0,
					Rate:      1,
					Tags:      gostatsd.Tags{"service.name:my-awesome-service"},
					Timestamp: gostatsd.Nanotime(time.Unix(100, 0).UnixNano()),
					Type:      gostatsd.GAUGE,
				})
				mm.Receive(&gostatsd.Metric{
					Name:      "my-metric",
					Value:     100.0,
					Rate:      1,
					Tags:      gostatsd.Tags{"service.name:my-awesome-service"},
					Timestamp: gostatsd.Nanotime(time.Unix(100, 0).UnixNano()),
					Type:      gostatsd.SET,
				})
				mm.Receive(&gostatsd.Metric{
					Name:      "my-metric",
					Value:     100.0,
					Rate:      1,
					Tags:      gostatsd.Tags{"service.name:my-awesome-service"},
					Timestamp: gostatsd.Nanotime(time.Unix(100, 0).UnixNano()),
					Type:      gostatsd.TIMER,
				})
				return mm
			}(),
			handler: func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				assert.NoError(t, err, "Must not error reading body")
				assert.NotEmpty(t, body, "Must not have an empty body")
			},
			enableHistograms: true,
			validate: func(t *testing.T) func(errs []error) {
				return func(errs []error) {
					if !assert.Len(t, errs, 0, "Must not error") {
						return
					}
				}
			},
		},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := httptest.NewServer(tc.handler)
			t.Cleanup(s.Close)

			v := viper.New()
			v.Set("otlp.endpoint", s.URL)
			if tc.enableHistograms {
				v.Set("otlp.conversion", ConversionAsHistogram)
			}

			logger := fixtures.NewTestLogger(t)

			b, err := NewClientFromViper(
				v,
				logger,
				transport.NewTransportPool(logger, v),
			)
			require.NoError(t, err, "Must not error creating backend")

			b.SendMetricsAsync(context.Background(), tc.mm, tc.validate(t))
		})
	}
}
