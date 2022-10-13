// Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
// SPDX-License-Identifier: MIT

package cloudwatch

import (
	"context"
	"log"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/service/cloudwatch"
	"github.com/aws/aws-sdk-go/service/cloudwatch/cloudwatchiface"
	"github.com/influxdata/telegraf"
	"github.com/influxdata/telegraf/models"
	"github.com/influxdata/telegraf/plugins/outputs"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/pdata/pmetric"

	"github.com/aws/private-amazon-cloudwatch-agent-staging/cfg/agentinfo"
	configaws "github.com/aws/private-amazon-cloudwatch-agent-staging/cfg/aws"
	"github.com/aws/private-amazon-cloudwatch-agent-staging/handlers"
	"github.com/aws/private-amazon-cloudwatch-agent-staging/internal/publisher"
	"github.com/aws/private-amazon-cloudwatch-agent-staging/internal/retryer"
)

const (
	defaultMaxDatumsPerCall               = 1000   // PutMetricData only supports up to 1000 data metrics per call by default
	defaultMaxValuesPerDatum              = 150    // By default only these number of values can be inserted into the value list
	bottomLinePayloadSizeInBytesToPublish = 999000 // 1MB payload size. Leave 1kb for the last datum buffer before applying compression ratio.
	metricChanBufferSize                  = 10000
	datumBatchChanBufferSize              = 50 // the number of requests we buffer
	maxConcurrentPublisher                = 10 // the number of CloudWatch clients send request concurrently
	defaultForceFlushInterval             = time.Minute
	highResolutionTagKey                  = "aws:StorageResolution"
	defaultRetryCount                     = 5 // this is the retry count, the total attempts would be retry count + 1 at most.
	backoffRetryBase                      = 200 * time.Millisecond
	MaxDimensions                         = 30
)

const (
	opPutLogEvents       = "PutLogEvents"
	opPutMetricData      = "PutMetricData"
	dropOriginalWildcard = "*"
)

type CloudWatch struct {
	config *Config
	svc    cloudwatchiface.CloudWatchAPI
	// todo: may want to increase the size of the chan since the type changed.
	// 1 telegraf Metric could have many Fields.
	// Each field corresponds to a MetricDatum.
	metricChan            chan *cloudwatch.MetricDatum
	datumBatchChan        chan []*cloudwatch.MetricDatum
	metricDatumBatch      *MetricDatumBatch
	shutdownChan          chan struct{}
	metricDecorations     *MetricDecorations
	retries               int
	publisher             *publisher.Publisher
	retryer               *retryer.LogThrottleRetryer
	droppingOriginMetrics map[string]map[string]struct{}
}

// Compile time interface check.
var _ component.MetricsExporter = (*CloudWatch)(nil)

func (c *CloudWatch) Capabilities() consumer.Capabilities {
	return consumer.Capabilities{MutatesData: false}
}

func (c *CloudWatch) Start(_ context.Context, host component.Host) error {
	var err error
	c.publisher, _ = publisher.NewPublisher(
		publisher.NewNonBlockingFifoQueue(metricChanBufferSize),
		maxConcurrentPublisher,
		2*time.Second,
		c.WriteToCloudWatch)
	c.metricDecorations, err = NewMetricDecorations(c.config.MetricDecorations)
	if err != nil {
		return err
	}
	credentialConfig := &configaws.CredentialConfig{
		Region:    c.config.Region,
		AccessKey: c.config.AccessKey,
		SecretKey: c.config.SecretKey,
		RoleARN:   c.config.RoleARN,
		Profile:   c.config.Profile,
		Filename:  c.config.SharedCredentialFilename,
		Token:     c.config.Token,
	}
	configProvider := credentialConfig.Credentials()
	logger := models.NewLogger("outputs", "cloudwatch", "")
	logThrottleRetryer := retryer.NewLogThrottleRetryer(logger)
	svc := cloudwatch.New(
		configProvider,
		&aws.Config{
			Endpoint: aws.String(c.config.EndpointOverride),
			Retryer:  logThrottleRetryer,
			LogLevel: configaws.SDKLogLevel(),
			Logger:   configaws.SDKLogger{},
		})
	svc.Handlers.Build.PushBackNamed(handlers.NewRequestCompressionHandler([]string{opPutLogEvents, opPutMetricData}))
	svc.Handlers.Build.PushBackNamed(handlers.NewCustomHeaderHandler("User-Agent", agentinfo.UserAgent("")))
	//Format unique roll up list
	c.config.RollupDimensions = GetUniqueRollupList(c.config.RollupDimensions)
	//Construct map for metrics that dropping origin
	c.droppingOriginMetrics = GetDroppingDimensionMap(c.config.DropOriginConfigs)
	c.svc = svc
	c.retryer = logThrottleRetryer
	c.startRoutines()
	return nil
}

func (c *CloudWatch) startRoutines() {
	c.metricChan = make(chan *cloudwatch.MetricDatum, metricChanBufferSize)
	c.datumBatchChan = make(chan []*cloudwatch.MetricDatum, datumBatchChanBufferSize)
	c.shutdownChan = make(chan struct{})
	setNewDistributionFunc(c.config.MaxValuesPerDatum)
	perRequestConstSize := overallConstPerRequestSize + len(c.config.Namespace) + namespaceOverheads
	c.metricDatumBatch = newMetricDatumBatch(c.config.MaxDatumsPerCall, perRequestConstSize)
	go c.pushMetricDatum()
	go c.publish()
}

func (c *CloudWatch) Shutdown(ctx context.Context) error {
	log.Println("D! Stopping the CloudWatch output plugin")
	for i := 0; i < 5; i++ {
		if len(c.metricChan) == 0 && len(c.datumBatchChan) == 0 {
			break
		} else {
			log.Printf("D! CloudWatch Close, %vth time to sleep since there is still some metric data remaining to publish.", i)
			time.Sleep(time.Second)
		}
	}
	if metricChanLen, datumBatchChanLen := len(c.metricChan), len(c.datumBatchChan); metricChanLen != 0 || datumBatchChanLen != 0 {
		log.Printf("D! CloudWatch Close, metricChan length = %v, datumBatchChan length = %v.", metricChanLen, datumBatchChanLen)
	}
	close(c.shutdownChan)
	c.publisher.Close()
	c.retryer.Stop()
	log.Println("D! Stopped the CloudWatch output plugin")
	return nil
}

// ConsumeMetrics queues metrics to be published to CW.
// The actual publishing will occur in a long running goroutine.
// This method can block when publishing is backed up.
func (c *CloudWatch) ConsumeMetrics(ctx context.Context, metrics pmetric.Metrics) error {
	datums := c.ConvertOtelMetrics(metrics)
	for _, d := range datums {
		c.metricChan <- d
	}
	return nil
}

// pushMetricDatum groups datums into batches for efficient API calls.
// When a batch is full it is queued up for sending.
// Even if the batch is not full it will still get sent after the flush interval.
func (c *CloudWatch) pushMetricDatum() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case datum := <-c.metricChan:
			c.metricDatumBatch.Partition = append(c.metricDatumBatch.Partition,
				datum)
			c.metricDatumBatch.Size += payload(datum)
			if c.metricDatumBatch.isFull() {
				c.datumBatchChan <- c.metricDatumBatch.Partition
				c.metricDatumBatch.clear()
			}
		case <-ticker.C:
			if c.timeToPublish(c.metricDatumBatch) {
				// if the time to publish comes
				c.datumBatchChan <- c.metricDatumBatch.Partition
				c.metricDatumBatch.clear()
			}
		case <-c.shutdownChan:
			return
		}
	}
}

type MetricDatumBatch struct {
	MaxDatumsPerCall    int
	Partition           []*cloudwatch.MetricDatum
	BeginTime           time.Time
	Size                int
	perRequestConstSize int
}

func newMetricDatumBatch(maxDatumsPerCall, perRequestConstSize int) *MetricDatumBatch {
	return &MetricDatumBatch{
		MaxDatumsPerCall:    maxDatumsPerCall,
		Partition:           make([]*cloudwatch.MetricDatum, 0, maxDatumsPerCall),
		BeginTime:           time.Now(),
		Size:                perRequestConstSize,
		perRequestConstSize: perRequestConstSize,
	}
}

func (b *MetricDatumBatch) clear() {
	b.Partition = make([]*cloudwatch.MetricDatum, 0, b.MaxDatumsPerCall)
	b.BeginTime = time.Now()
	b.Size = b.perRequestConstSize
}

func (b *MetricDatumBatch) isFull() bool {
	return len(b.Partition) >= b.MaxDatumsPerCall || b.Size >= bottomLinePayloadSizeInBytesToPublish
}

func (c *CloudWatch) timeToPublish(b *MetricDatumBatch) bool {
	return len(b.Partition) > 0 && time.Now().Sub(b.BeginTime) >= c.config.ForceFlushInterval
}

// getFirstPushMs returns the time at which the first upload should occur.
// It uses random jitter as an offset from the start of the given interval.
func getFirstPushMs(interval time.Duration) int64 {
	publishJitter := publishJitter(interval)
	log.Printf("I! cloudwatch: publish with ForceFlushInterval: %v, Publish Jitter: %v",
		interval, publishJitter)
	nowMs := time.Now().UnixMilli()
	// Truncate i.e. round down, then add jitter.
	// If the rounded down time is in the past, move it forward.
	nextMs := nowMs - (nowMs % interval.Milliseconds()) + publishJitter.Milliseconds()
	if nextMs < nowMs {
		nextMs += interval.Milliseconds()
	}
	return nextMs
}

// publish loops until a shutdown occurs.
// It periodically tries pushing batches of metrics (if there are any).
// If thet batch buffer fills up the interval will be gradually reduced to avoid
// many agents bursting the backend.
func (c *CloudWatch) publish() {
	currentInterval := c.config.ForceFlushInterval
	nextMs := getFirstPushMs(currentInterval)
	bufferFullOccurred := false

	for {
		shouldPublish := false
		select {
		case <-c.shutdownChan:
			log.Printf("D! cloudwatch: publish routine receives the shutdown signal, exiting.")
			return
		default:
		}

		nowMs := time.Now().UnixMilli()

		if c.metricDatumBatchFull() {
			if !bufferFullOccurred {
				// Set to true so this only happens once per push.
				bufferFullOccurred = true
				// Keep interval above above 1 second.
				if currentInterval.Seconds() > 1 {
					currentInterval /= 2
					if currentInterval.Seconds() < 1 {
						currentInterval = 1 * time.Second
					}
					// Cut the remaining interval in half.
					nextMs = nowMs + ((nextMs - nowMs) / 2)
				}
			}
		}

		if nowMs >= nextMs {
			shouldPublish = true
			// Restore interval if buffer did not fill up during this interval.
			if !bufferFullOccurred {
				currentInterval = c.config.ForceFlushInterval
			}
			nextMs += currentInterval.Milliseconds()
		}

		if shouldPublish {
			c.pushMetricDatumBatch()
			bufferFullOccurred = false
		}
		// Sleep 1 second, unless the nextMs is less than a second away.
		if nextMs-nowMs > time.Second.Milliseconds() {
			time.Sleep(time.Second)
		} else {
			time.Sleep(time.Duration(nextMs-nowMs) * time.Millisecond)
		}
	}
}

// metricDatumBatchFull returns true if the channel/buffer of batches if full.
func (c *CloudWatch) metricDatumBatchFull() bool {
	return len(c.datumBatchChan) >= datumBatchChanBufferSize
}

// pushMetricDatumBatch will try receiving on the channel, and if successful,
// then it publishes the received batch.
func (c *CloudWatch) pushMetricDatumBatch() {
	for {
		select {
		case datumBatch := <-c.datumBatchChan:
			c.publisher.Publish(datumBatch)
			continue
		default:
		}
		break
	}
}

// backoffSleep sleeps some amount of time based on number of retries done.
func (c *CloudWatch) backoffSleep() {
	d := 1 * time.Minute
	if c.retries <= defaultRetryCount {
		d = backoffRetryBase * time.Duration(1<<c.retries)
	}
	d = (d / 2) + publishJitter(d/2)
	log.Printf("W! cloudwatch: %v retries, going to sleep %v ms before retrying.",
		c.retries, d.Milliseconds())
	c.retries++
	time.Sleep(d)
}

func (c *CloudWatch) WriteToCloudWatch(req interface{}) {
	datums := req.([]*cloudwatch.MetricDatum)
	params := &cloudwatch.PutMetricDataInput{
		MetricData: datums,
		Namespace:  aws.String(c.config.Namespace),
	}
	var err error
	for i := 0; i < defaultRetryCount; i++ {
		_, err = c.svc.PutMetricData(params)
		if err != nil {
			awsErr, ok := err.(awserr.Error)
			if !ok {
				log.Printf("E! cloudwatch: Cannot cast PutMetricData error %v into awserr.Error.", err)
				c.backoffSleep()
				continue
			}
			switch awsErr.Code() {
			case cloudwatch.ErrCodeLimitExceededFault, cloudwatch.ErrCodeInternalServiceFault:
				log.Printf("W! cloudwatch: PutMetricData, error: %s, message: %s",
					awsErr.Code(),
					awsErr.Message())
				c.backoffSleep()
				continue

			default:
				log.Printf("E! cloudwatch: code: %s, message: %s, original error: %+v", awsErr.Code(), awsErr.Message(), awsErr.OrigErr())
				c.backoffSleep()
			}
		} else {
			c.retries = 0
		}
		break
	}
	if err != nil {
		log.Println("E! cloudwatch: WriteToCloudWatch failure, err: ", err)
	}
}

func (c *CloudWatch) decorateMetricName(category string, name string) (decoratedName string) {
	if c.metricDecorations != nil {
		decoratedName = c.metricDecorations.getRename(category, name)
	}
	if decoratedName == "" {
		if name == "value" {
			decoratedName = category
		} else {
			separator := "_"
			if runtime.GOOS == "windows" {
				separator = " "
			}
			decoratedName = strings.Join([]string{category, name}, separator)
		}
	}
	return
}

func (c *CloudWatch) decorateMetricUnit(category string, name string) (decoratedUnit string) {
	if c.metricDecorations != nil {
		decoratedUnit = c.metricDecorations.getUnit(category, name)
	}
	return
}

// sortedTagKeys returns a sorted list of keys in the map.
// Necessary for comparing a metric-name and its dimensions to determine
// if 2 metrics are actually the same.
func sortedTagKeys(tagMap map[string]string) []string {
	// Allocate slice with proper size and avoid append.
	keys := make([]string, 0, len(tagMap))
	for k := range tagMap {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// BuildDimensions converts the given map of strings to a list of dimensions.
// CloudWatch supports up to 30 dimensions per metric.
// So keep up to the first 30 alphabetically.
// This always includes the "host" tag if it exists.
// See https://github.com/aws/amazon-cloudwatch-agent/issues/398
func BuildDimensions(tagMap map[string]string) []*cloudwatch.Dimension {
	if len(tagMap) > MaxDimensions {
		log.Printf("D! cloudwatch: dropping dimensions, max %v, count %v",
			MaxDimensions, len(tagMap))
	}
	dimensions := make([]*cloudwatch.Dimension, 0, MaxDimensions)
	// This is pretty ugly but we always want to include the "host" tag if it exists.
	if host, ok := tagMap["host"]; ok && host != "" {
		dimensions = append(dimensions, &cloudwatch.Dimension{
			Name:  aws.String("host"),
			Value: aws.String(host),
		})
	}
	sortedKeys := sortedTagKeys(tagMap)
	for _, k := range sortedKeys {
		if len(dimensions) >= MaxDimensions {
			break
		}
		if k == "host" {
			continue
		}
		value := tagMap[k]
		if value == "" {
			continue
		}
		dimensions = append(dimensions, &cloudwatch.Dimension{
			Name:  aws.String(k),
			Value: aws.String(tagMap[k]),
		})
	}
	return dimensions
}

func (c *CloudWatch) ProcessRollup(rawDimension []*cloudwatch.Dimension) [][]*cloudwatch.Dimension {
	rawDimensionMap := map[string]string{}
	for _, v := range rawDimension {
		rawDimensionMap[*v.Name] = *v.Value
	}
	targetDimensionsList := c.config.RollupDimensions
	fullDimensionsList := [][]*cloudwatch.Dimension{rawDimension}
	for _, targetDimensions := range targetDimensionsList {
		i := 0
		extraDimensions := make([]*cloudwatch.Dimension, len(targetDimensions))
		for _, targetDimensionKey := range targetDimensions {
			if val, ok := rawDimensionMap[targetDimensionKey]; !ok {
				break
			} else {
				extraDimensions[i] = &cloudwatch.Dimension{
					Name:  aws.String(targetDimensionKey),
					Value: aws.String(val),
				}
			}
			i += 1
		}
		if i == len(targetDimensions) && !reflect.DeepEqual(rawDimension, extraDimensions) {
			fullDimensionsList = append(fullDimensionsList, extraDimensions)
		}
	}
	return fullDimensionsList
}

func GetUniqueRollupList(inputLists [][]string) [][]string {
	uniqueLists := [][]string{}
	if len(inputLists) > 0 {
		uniqueLists = append(uniqueLists, inputLists[0])
	}
	for _, inputList := range inputLists {
		count := 0
		for _, u := range uniqueLists {
			if reflect.DeepEqual(inputList, u) {
				break
			}
			count += 1
			if count == len(uniqueLists) {
				uniqueLists = append(uniqueLists, inputList)
			}
		}
	}
	log.Printf("I! cloudwatch: get unique roll up list %v", uniqueLists)
	return uniqueLists
}

func (c *CloudWatch) IsDropping(metricName string, dimensionName string) bool {
	if droppingDimensions, ok := c.droppingOriginMetrics[metricName]; ok {
		if _, droppingAll := droppingDimensions[dropOriginalWildcard]; droppingAll {
			return true
		}
		_, dropping := droppingDimensions[dimensionName]
		return dropping
	}
	return false
}

func GetDroppingDimensionMap(input map[string][]string) map[string]map[string]struct{} {
	result := make(map[string]map[string]struct{})
	for k, v := range input {
		result[k] = make(map[string]struct{})
		for _, dimension := range v {
			result[k][dimension] = struct{}{}
		}
	}
	return result
}

func (c *CloudWatch) SampleConfig() string {
	return ""
}

func (c *CloudWatch) Description() string {
	return "Configuration for AWS CloudWatch output."
}

func (c *CloudWatch) Connect() error {
	return nil
}

func (c *CloudWatch) Close() error {
	return nil
}

func (c *CloudWatch) Write(metrics []telegraf.Metric) error {
	return nil
}

func init() {
	outputs.Add("cloudwatch", func() telegraf.Output {
		return &CloudWatch{}
	})
}
