package otel

import (
	"errors"
	"fmt"

	"github.com/open-telemetry/opentelemetry-collector-contrib/processor/cumulativetodeltaprocessor"
	"go.opentelemetry.io/collector/config"
	"go.opentelemetry.io/collector/config/configtelemetry"
	"go.opentelemetry.io/collector/confmap"
	"go.opentelemetry.io/collector/processor/batchprocessor"
	"go.opentelemetry.io/collector/service"
	"go.opentelemetry.io/collector/service/telemetry"
	"go.uber.org/multierr"
	"go.uber.org/zap/zapcore"

	"github.com/aws/private-amazon-cloudwatch-agent-staging/translator/translate/otel/common"
	"github.com/aws/private-amazon-cloudwatch-agent-staging/translator/translate/otel/exporter/awscloudwatch"
	"github.com/aws/private-amazon-cloudwatch-agent-staging/translator/translate/otel/pipeline"
	"github.com/aws/private-amazon-cloudwatch-agent-staging/translator/translate/otel/processor"
	"github.com/aws/private-amazon-cloudwatch-agent-staging/translator/translate/otel/receiver/adapter"
	"github.com/aws/private-amazon-cloudwatch-agent-staging/translator/util"
)

// Translator is used to create an OTEL config.
type Translator struct {
	pipelineTranslator   common.Translator[common.Pipelines]
	receiverTranslators  common.TranslatorMap[config.Receiver]
	processorTranslators common.TranslatorMap[config.Processor]
	exporterTranslators  common.TranslatorMap[config.Exporter]
}

// NewTranslator creates a new Translator.
func NewTranslator() *Translator {
	return &Translator{
		pipelineTranslator: pipeline.NewTranslator(),
		receiverTranslators: common.NewTranslatorMap[config.Receiver](
			adapter.NewTranslator("cpu", common.ConfigKey(common.MetricsKey, common.MetricsCollectedKey, "cpu")),
		),
		processorTranslators: common.NewTranslatorMap(
			processor.NewDefaultTranslator(batchprocessor.NewFactory()),
			processor.NewDefaultTranslator(cumulativetodeltaprocessor.NewFactory()),
		),
		exporterTranslators: common.NewTranslatorMap(
			awscloudwatch.NewTranslator(),
		),
	}
}

// Translate converts a JSON config into an OTEL config.
func (t *Translator) Translate(jsonConfig interface{}) (*service.Config, error) {
	m, ok := jsonConfig.(map[string]interface{})
	if !ok {
		return nil, errors.New("invalid json config")
	}
	conf := confmap.NewFromStringMap(m)
	pipelines, err := t.pipelineTranslator.Translate(conf)
	if err != nil {
		return nil, fmt.Errorf("unable to translate pipelines: %w", err)
	}
	cfg := &service.Config{
		Receivers:  map[config.ComponentID]config.Receiver{},
		Exporters:  map[config.ComponentID]config.Exporter{},
		Processors: map[config.ComponentID]config.Processor{},
		Service: service.ConfigService{
			Telemetry: telemetry.Config{
				Logs:    telemetry.LogsConfig{Level: zapcore.InfoLevel},
				Metrics: telemetry.MetricsConfig{Level: configtelemetry.LevelNone},
			},
			Pipelines: pipelines,
		},
	}
	if err = t.buildComponents(cfg, conf); err != nil {
		return nil, fmt.Errorf("unable to build components in pipeline: %w", err)
	}
	if err = cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid otel config: %w", err)
	}
	return cfg, nil
}

// buildComponents uses the pipelines defined in the config to build the components.
func (t *Translator) buildComponents(cfg *service.Config, conf *confmap.Conf) error {
	var errs error
	receivers := util.NewSet[config.ComponentID]()
	processors := util.NewSet[config.ComponentID]()
	exporters := util.NewSet[config.ComponentID]()
	for _, p := range cfg.Pipelines {
		receivers.Add(p.Receivers...)
		processors.Add(p.Processors...)
		exporters.Add(p.Exporters...)
	}
	errs = multierr.Append(errs, buildComponents(conf, receivers, cfg.Receivers, t.receiverTranslators.Get))
	errs = multierr.Append(errs, buildComponents(conf, processors, cfg.Processors, t.processorTranslators.Get))
	errs = multierr.Append(errs, buildComponents(conf, exporters, cfg.Exporters, t.exporterTranslators.Get))
	return errs
}

// buildComponents attempts to translate a component for each ID in the set.
func buildComponents[C common.Identifiable](
	conf *confmap.Conf,
	ids util.Set[config.ComponentID],
	components map[config.ComponentID]C,
	getTranslator func(config.Type) (common.Translator[C], bool),
) error {
	var errs error
	for id := range ids {
		if translator, ok := getTranslator(id.Type()); ok {
			cfg, err := translator.Translate(conf)
			if err != nil {
				errs = multierr.Append(errs, err)
				continue
			}
			cfg.SetIDName(id.Name())
			components[cfg.ID()] = cfg
		}
	}
	return errs
}
