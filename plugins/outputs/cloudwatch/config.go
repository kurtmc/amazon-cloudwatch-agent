// Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
// SPDX-License-Identifier: MIT

package cloudwatch

import (
	"errors"
	"time"

	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/resourcetotelemetry"
	"go.opentelemetry.io/collector/config"
)

// Config represent a configuration for the CloudWatch logs exporter.
type Config struct {
	// Squash ensures fields are correctly decoded in embedded struct.
	config.ExporterSettings  `mapstructure:",squash"`
	Region                   string                   `mapstructure:"region"`
	EndpointOverride         string                   `mapstructure:"endpoint_override,omitempty"`
	AccessKey                string                   `mapstructure:"access_key,omitempty"`
	SecretKey                string                   `mapstructure:"secret_key,omitempty"`
	RoleARN                  string                   `mapstructure:"role_arn,omitempty"`
	Profile                  string                   `mapstructure:"profile,omitempty"`
	SharedCredentialFilename string                   `mapstructure:"shared_credential_file,omitempty"`
	Token                    string                   `mapstructure:"token,omitempty"`
	ForceFlushInterval       time.Duration            `mapstructure:"force_flush_interval"`
	MaxDatumsPerCall         int                      `mapstructure:"max_datums_per_call"`
	MaxValuesPerDatum        int                      `mapstructure:"max_values_per_datum"`
	MetricDecorations        []MetricDecorationConfig `mapstructure:"metric_decoration,omitempty"`
	RollupDimensions         [][]string               `mapstructure:"rollup_dimensions,omitempty"`
	DropOriginConfigs        map[string][]string      `mapstructure:"drop_original_metrics,omitempty"`
	Namespace                string                   `mapstructure:"namespace"`

	// ResourceToTelemetrySettings is the option for converting resource
	// attributes to telemetry attributes.
	// "Enabled" - A boolean field to enable/disable this option. Default is `false`.
	// If enabled, all the resource attributes will be converted to metric labels by default.
	ResourceToTelemetrySettings resourcetotelemetry.Settings `mapstructure:"resource_to_telemetry_conversion"`
}

// Verify Config implements Exporter interface.
var _ config.Exporter = (*Config)(nil)

// Validate checks if the exporter configuration is valid.
func (c *Config) Validate() error {
	if c.Region == "" {
		return errors.New("'region' must be set")
	}
	if c.Namespace == "" {
		return errors.New("'namespace' must be set")
	}
	if c.ForceFlushInterval < time.Millisecond {
		return errors.New("'force_flush_interval' must be at least 1 millisecond")
	}
	return nil
}